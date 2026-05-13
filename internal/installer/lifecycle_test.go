package installer

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/catalog"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/github"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
)

// serveBundle starts an httptest TLS server that serves the asset ZIP
// at /<assetName> and the digest at /<assetName>.sha256, then returns
// the asset URL, checksum URL, and the configured HTTP client.
func serveBundle(t *testing.T, assetName string, zipBytes []byte) (assetURL, sumURL string, client *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	})
	mux.HandleFunc("/"+assetName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha256Hex(zipBytes) + "\n"))
	})
	base, c := startServer(t, mux)
	return base + "/" + assetName, base + "/" + assetName + ".sha256", c
}

func makePlan(name, version, assetURL, sumURL string) Plan {
	tag := "v" + version
	assetBase := strings.TrimPrefix(filepath.Base(assetURL), "")
	sumBase := filepath.Base(sumURL)
	return Plan{
		Name:  name,
		Entry: catalog.Entry{Name: name, Repository: "test/owner-repo"}, // pattern empty => default
		Release: github.Release{
			TagName: tag,
			Assets: []github.Asset{
				{Name: assetBase, BrowserDownloadURL: assetURL, Size: 1024},
				{Name: sumBase, BrowserDownloadURL: sumURL, Size: 65},
			},
		},
	}
}

func TestInstall_HappyPath(t *testing.T) {
	const name = "test-plugin"
	const version = "1.0.0"
	zipBytes := buildBundle(t, name, version, 1)
	assetName := name + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"

	assetURL, sumURL, client := serveBundle(t, assetName, zipBytes)
	pluginsDir := t.TempDir()
	in := &Installer{
		PluginsDir:   pluginsDir,
		HTTPClient:   client,
		Log:          logging.Nop{},
		HostAPIMajor: 1,
	}
	plan := makePlan(name, version, assetURL, sumURL)

	if err := in.Install(context.Background(), plan); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Target dir exists with binary + manifest.
	target := filepath.Join(pluginsDir, name)
	if _, err := os.Stat(filepath.Join(target, binaryName(name))); err != nil {
		t.Errorf("binary missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "manifest.toml")); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

func TestInstall_RejectsExistingTarget(t *testing.T) {
	const name = "test-plugin"
	const version = "1.0.0"
	zipBytes := buildBundle(t, name, version, 1)
	assetName := name + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	assetURL, sumURL, client := serveBundle(t, assetName, zipBytes)

	pluginsDir := t.TempDir()
	// Pre-create the target directory.
	if err := os.MkdirAll(filepath.Join(pluginsDir, name), 0o755); err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	in := &Installer{PluginsDir: pluginsDir, HTTPClient: client, Log: logging.Nop{}, HostAPIMajor: 1}
	err := in.Install(context.Background(), makePlan(name, version, assetURL, sumURL))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("Install over existing: err=%v, want 'already exists'", err)
	}
}

func TestUpdate_SwapsAndCleansUpOldDir(t *testing.T) {
	const name = "test-plugin"
	zipV1 := buildBundle(t, name, "1.0.0", 1)
	zipV2 := buildBundle(t, name, "1.1.0", 1)
	asset1 := name + "_1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	asset2 := name + "_1.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"

	// Serve both versions on one TLS server using a mux.
	mux := http.NewServeMux()
	mux.HandleFunc("/"+asset1, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipV1) })
	mux.HandleFunc("/"+asset1+".sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha256Hex(zipV1)))
	})
	mux.HandleFunc("/"+asset2, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipV2) })
	mux.HandleFunc("/"+asset2+".sha256", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sha256Hex(zipV2)))
	})
	base, client := startServer(t, mux)

	pluginsDir := t.TempDir()
	in := &Installer{PluginsDir: pluginsDir, HTTPClient: client, Log: logging.Nop{}, HostAPIMajor: 1}

	planV1 := makePlan(name, "1.0.0", base+"/"+asset1, base+"/"+asset1+".sha256")
	if err := in.Install(context.Background(), planV1); err != nil {
		t.Fatalf("Install v1: %v", err)
	}

	planV2 := makePlan(name, "1.1.0", base+"/"+asset2, base+"/"+asset2+".sha256")
	if err := in.Update(context.Background(), planV2); err != nil {
		t.Fatalf("Update v2: %v", err)
	}

	// Manifest should now reflect v1.1.0.
	got, err := os.ReadFile(filepath.Join(pluginsDir, name, "manifest.toml"))
	if err != nil {
		t.Fatalf("read manifest after update: %v", err)
	}
	if !strings.Contains(string(got), `version = "1.1.0"`) {
		t.Errorf("manifest after update = %q, want version 1.1.0", got)
	}

	// .old must be cleaned up.
	if _, err := os.Stat(filepath.Join(pluginsDir, name+".old")); !os.IsNotExist(err) {
		t.Errorf("%s.old still exists: %v", name, err)
	}
}

func TestUpdate_NotInstalledIsError(t *testing.T) {
	const name = "test-plugin"
	const version = "1.0.0"
	zipBytes := buildBundle(t, name, version, 1)
	assetName := name + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	assetURL, sumURL, client := serveBundle(t, assetName, zipBytes)

	in := &Installer{PluginsDir: t.TempDir(), HTTPClient: client, Log: logging.Nop{}, HostAPIMajor: 1}
	err := in.Update(context.Background(), makePlan(name, version, assetURL, sumURL))
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("Update without install: err=%v, want 'not installed'", err)
	}
}

func TestUninstall_RemovesDirectory(t *testing.T) {
	const name = "test-plugin"
	pluginsDir := t.TempDir()
	target := filepath.Join(pluginsDir, name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	writeFile(t, filepath.Join(target, "some-file"), []byte("payload"))

	in := &Installer{PluginsDir: pluginsDir, Log: logging.Nop{}}
	if err := in.Uninstall(context.Background(), name); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target still exists after uninstall: %v", err)
	}
}

func TestUninstall_IsIdempotent(t *testing.T) {
	in := &Installer{PluginsDir: t.TempDir(), Log: logging.Nop{}}
	if err := in.Uninstall(context.Background(), "never-installed"); err != nil {
		t.Errorf("Uninstall of missing plugin: %v, want nil (idempotent)", err)
	}
}

func TestInstall_ChecksumMismatch_LeavesNoTarget(t *testing.T) {
	const name = "test-plugin"
	const version = "1.0.0"
	zipBytes := buildBundle(t, name, version, 1)
	assetName := name + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"

	mux := http.NewServeMux()
	mux.HandleFunc("/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	})
	mux.HandleFunc("/"+assetName+".sha256", func(w http.ResponseWriter, r *http.Request) {
		// Wrong digest on purpose.
		_, _ = w.Write([]byte("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\n"))
	})
	base, client := startServer(t, mux)

	pluginsDir := t.TempDir()
	in := &Installer{PluginsDir: pluginsDir, HTTPClient: client, Log: logging.Nop{}, HostAPIMajor: 1}
	err := in.Install(context.Background(), makePlan(name, version, base+"/"+assetName, base+"/"+assetName+".sha256"))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Install with bad sha256: err=%v, want 'checksum mismatch'", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, name)); !os.IsNotExist(err) {
		t.Errorf("target dir exists after checksum failure: %v", err)
	}
}

func TestInstall_RejectsTopLevelMismatch(t *testing.T) {
	const name = "test-plugin"
	const version = "1.0.0"
	// Build a bundle whose top-level entry is "other-plugin/" instead of "test-plugin/".
	zipBytes := buildBundle(t, "other-plugin", version, 1)
	assetName := name + "_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"

	assetURL, sumURL, client := serveBundle(t, assetName, zipBytes)
	pluginsDir := t.TempDir()
	in := &Installer{PluginsDir: pluginsDir, HTTPClient: client, Log: logging.Nop{}, HostAPIMajor: 1}

	err := in.Install(context.Background(), makePlan(name, version, assetURL, sumURL))
	if err == nil || !strings.Contains(err.Error(), "top-level") {
		t.Errorf("Install with top-level mismatch: err=%v, want top-level error", err)
	}
}
