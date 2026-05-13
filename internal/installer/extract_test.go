package installer

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildZipFromEntries writes a ZIP containing the named entries with the
// given content. Name slashes are written verbatim (no path cleaning) so
// callers can construct Zip-Slip attack payloads.
func buildZipFromEntries(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatalf("zip.Create %q: %v", name, err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func writeBundle(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bundle.zip")
	writeFile(t, p, b)
	return p
}

func TestUnzipSafe_HappyPath_NestedDirs(t *testing.T) {
	zipBytes := buildZipFromEntries(t, map[string]string{
		"plug/file-a.txt":            "alpha",
		"plug/sub/file-b.txt":        "bravo",
		"plug/sub/deeper/file-c.txt": "charlie",
	})
	zipPath := writeBundle(t, zipBytes)
	dest := filepath.Join(t.TempDir(), "out")

	if err := unzipSafe(zipPath, dest); err != nil {
		t.Fatalf("unzipSafe: %v", err)
	}
	for rel, want := range map[string]string{
		"plug/file-a.txt":            "alpha",
		"plug/sub/file-b.txt":        "bravo",
		"plug/sub/deeper/file-c.txt": "charlie",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("ReadFile %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("file %s content = %q, want %q", rel, got, want)
		}
	}
}

func TestUnzipSafe_RejectsParentEscape(t *testing.T) {
	zipBytes := buildZipFromEntries(t, map[string]string{
		"../escaped.txt": "should not be written",
	})
	zipPath := writeBundle(t, zipBytes)
	dest := filepath.Join(t.TempDir(), "out")

	err := unzipSafe(zipPath, dest)
	if err == nil {
		t.Fatalf("unzipSafe: nil error, want zip-slip rejection")
	}
	if !strings.Contains(err.Error(), "zip-slip") {
		t.Errorf("err message = %q, want mention zip-slip", err.Error())
	}
}

func TestUnzipSafe_RejectsAbsolutePath(t *testing.T) {
	// "/etc/passwd" on POSIX or "C:/Windows/foo" on Windows.
	var abs string
	if filepath.Separator == '\\' {
		abs = "C:/poc.txt"
	} else {
		abs = "/poc.txt"
	}
	zipBytes := buildZipFromEntries(t, map[string]string{abs: "evil"})
	zipPath := writeBundle(t, zipBytes)
	dest := filepath.Join(t.TempDir(), "out")

	err := unzipSafe(zipPath, dest)
	if err == nil {
		t.Fatalf("unzipSafe: nil error, want absolute-path rejection")
	}
}

func TestUnzipSafe_RejectsSymlinkEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "linky"}
	hdr.SetMode(os.ModeSymlink | 0o755)
	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	_, _ = fw.Write([]byte("/etc/passwd"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	zipPath := writeBundle(t, buf.Bytes())
	dest := filepath.Join(t.TempDir(), "out")

	err = unzipSafe(zipPath, dest)
	if err == nil {
		t.Fatalf("unzipSafe: nil error, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err message = %q, want mention symlink", err.Error())
	}
}
