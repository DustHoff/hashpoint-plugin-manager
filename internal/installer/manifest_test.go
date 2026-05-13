package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makePluginDir creates a temporary plugin directory layout with a
// binary, manifest.toml, and the given manifest body. Returns the
// directory path.
func makePluginDir(t *testing.T, name, manifestBody string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, binaryName(name)), []byte("synthetic"))
	writeFile(t, filepath.Join(root, "manifest.toml"), []byte(manifestBody))
	return root
}

func TestCrossCheckManifest_HappyPath(t *testing.T) {
	mf := `name = "test-plugin"
version = "1.0.0"
api_version = 1
capabilities = ["plugin_management"]
`
	root := makePluginDir(t, "test-plugin", mf)
	if err := crossCheckManifest(root, "test-plugin", 1); err != nil {
		t.Errorf("crossCheckManifest: %v, want nil", err)
	}
}

func TestCrossCheckManifest_NameMismatch(t *testing.T) {
	mf := `name = "wrong-name"
version = "1.0.0"
api_version = 1
`
	root := makePluginDir(t, "test-plugin", mf)
	err := crossCheckManifest(root, "test-plugin", 1)
	if err == nil || !strings.Contains(err.Error(), "manifest name") {
		t.Errorf("err = %v, want manifest-name-mismatch error", err)
	}
}

func TestCrossCheckManifest_VersionMajorMismatch(t *testing.T) {
	mf := `name = "test-plugin"
version = "2.0.0"
api_version = 2
`
	root := makePluginDir(t, "test-plugin", mf)
	err := crossCheckManifest(root, "test-plugin", 1)
	if err == nil || !strings.Contains(err.Error(), "manifest major") {
		t.Errorf("err = %v, want major-mismatch error", err)
	}
}

func TestCrossCheckManifest_InvalidSemver(t *testing.T) {
	mf := `name = "test-plugin"
version = "not-a-version"
api_version = 1
`
	root := makePluginDir(t, "test-plugin", mf)
	err := crossCheckManifest(root, "test-plugin", 1)
	if err == nil || !strings.Contains(err.Error(), "not valid semver") {
		t.Errorf("err = %v, want semver-validation error", err)
	}
}

func TestCrossCheckManifest_MissingManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "test-plugin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, binaryName("test-plugin")), []byte("synthetic"))

	err := crossCheckManifest(root, "test-plugin", 1)
	if err == nil || !strings.Contains(err.Error(), "manifest.toml missing") {
		t.Errorf("err = %v, want missing-manifest error", err)
	}
}

func TestCrossCheckManifest_MissingBinary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "test-plugin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mf := `name = "test-plugin"
version = "1.0.0"
api_version = 1
`
	writeFile(t, filepath.Join(root, "manifest.toml"), []byte(mf))

	err := crossCheckManifest(root, "test-plugin", 1)
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("err = %v, want missing-binary error", err)
	}
}

func TestCrossCheckManifest_NotADirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, p, []byte("file, not directory"))

	err := crossCheckManifest(p, "test-plugin", 1)
	if err == nil {
		t.Errorf("crossCheckManifest: nil, want directory error")
	}
}

// keep fmt referenced (used by table cases in earlier drafts; harmless)
var _ = fmt.Sprintf
