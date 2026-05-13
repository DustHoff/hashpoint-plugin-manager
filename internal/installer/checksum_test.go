package installer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDigestFile_SingleHexLine(t *testing.T) {
	dir := t.TempDir()
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p := filepath.Join(dir, "asset.zip.sha256")
	writeFile(t, p, []byte(digest+"\n"))

	got, err := parseDigestFile(p, "asset.zip")
	if err != nil {
		t.Fatalf("parseDigestFile: %v", err)
	}
	if got != digest {
		t.Errorf("got %q, want %q", got, digest)
	}
}

func TestParseDigestFile_SHA256SumFormat(t *testing.T) {
	dir := t.TempDir()
	want := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	other := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	body := want + "  asset.zip\n" + other + "  other.zip\n"
	p := filepath.Join(dir, "checksums.txt")
	writeFile(t, p, []byte(body))

	got, err := parseDigestFile(p, "asset.zip")
	if err != nil {
		t.Fatalf("parseDigestFile: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDigestFile_BinaryModeAsterisk(t *testing.T) {
	dir := t.TempDir()
	want := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	body := want + " *asset.zip\n"
	p := filepath.Join(dir, "checksums.txt")
	writeFile(t, p, []byte(body))

	got, err := parseDigestFile(p, "asset.zip")
	if err != nil {
		t.Fatalf("parseDigestFile: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDigestFile_BasenameMatchOnPathPrefix(t *testing.T) {
	dir := t.TempDir()
	want := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	body := want + "  ./dist/asset.zip\n"
	p := filepath.Join(dir, "checksums.txt")
	writeFile(t, p, []byte(body))

	got, err := parseDigestFile(p, "asset.zip")
	if err != nil {
		t.Fatalf("parseDigestFile: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDigestFile_MissingEntry(t *testing.T) {
	dir := t.TempDir()
	body := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee  unrelated.zip\n"
	p := filepath.Join(dir, "checksums.txt")
	writeFile(t, p, []byte(body))

	_, err := parseDigestFile(p, "asset.zip")
	if err == nil {
		t.Fatalf("parseDigestFile: nil error, want missing-entry error")
	}
	if !strings.Contains(err.Error(), "no entry for asset.zip") {
		t.Errorf("err message = %q, want mention asset.zip", err.Error())
	}
}

func TestVerifyChecksum_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello hashpoint")
	archive := filepath.Join(dir, "asset.zip")
	writeFile(t, archive, content)

	digest := sha256Hex(content)
	digestPath := filepath.Join(dir, "asset.zip.sha256")
	writeFile(t, digestPath, []byte(digest+"\n"))

	if err := verifyChecksum(archive, "asset.zip", digestPath); err != nil {
		t.Errorf("verifyChecksum: %v, want nil", err)
	}
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "asset.zip")
	writeFile(t, archive, []byte("real content"))

	wrongDigest := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	digestPath := filepath.Join(dir, "asset.zip.sha256")
	writeFile(t, digestPath, []byte(wrongDigest+"\n"))

	err := verifyChecksum(archive, "asset.zip", digestPath)
	if err == nil {
		t.Errorf("verifyChecksum: nil error, want mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("err message = %q, want mention checksum mismatch", err.Error())
	}
}

func TestIsHex64(t *testing.T) {
	good := "0123456789abcdefABCDEF0123456789abcdefABCDEF0123456789abcdefABCD"
	if !isHex64(good) {
		t.Errorf("isHex64(good) = false, want true")
	}
	if isHex64("short") {
		t.Errorf("isHex64(short) = true, want false")
	}
	if isHex64(good[:63] + "X") {
		t.Errorf("isHex64(non-hex char) = true, want false")
	}
}
