package installer

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// startServer wraps httptest.NewTLSServer so installer HTTP-fetch paths
// can reach loopback URLs over TLS with the test cert trusted.
func startServer(t *testing.T, h http.Handler) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client()
}

// binaryName returns the platform-appropriate executable name for a plugin.
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// buildBundle constructs a minimal GoReleaser-style ZIP bundle:
//
//	<name>/<binary>
//	<name>/manifest.toml
//
// The manifest is written with major == hostMajor so crossCheckManifest
// passes; manifestVersion lets callers exercise version-mismatch cases.
func buildBundle(t *testing.T, name, manifestVersion string, hostMajor int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Binary — content is arbitrary; only its existence and name matter.
	fw, err := zw.Create(name + "/" + binaryName(name))
	if err != nil {
		t.Fatalf("zip.Create binary: %v", err)
	}
	if _, err := fw.Write([]byte("synthetic-binary-payload")); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	// Manifest.
	mw, err := zw.Create(name + "/manifest.toml")
	if err != nil {
		t.Fatalf("zip.Create manifest: %v", err)
	}
	mf := fmt.Sprintf(`name = %q
version = %q
api_version = %d
description = "synthetic test plugin"
capabilities = ["plugin_management"]
`, name, manifestVersion, hostMajor)
	if _, err := mw.Write([]byte(mf)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// sha256Hex returns the lowercase hex SHA-256 of b.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// writeFile creates path with content and the typical permission bits.
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
