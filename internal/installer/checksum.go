package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/github"
)

// findChecksumAsset locates the .sha256 companion for the named asset.
// Priority order matches spec §6.2 step 6:
//  1. Exact match `<asset>.sha256` in the release assets.
//  2. Failing that, exactly one `*.sha256` file in the release (likely a
//     combined checksums file).
//
// Anything else is a hard fail (no .sha256, or ambiguous multiple).
func findChecksumAsset(assets []github.Asset, assetName string) (github.Asset, error) {
	want := assetName + ".sha256"
	var fallbacks []github.Asset
	for _, a := range assets {
		if a.Name == want {
			return a, nil
		}
		if strings.HasSuffix(strings.ToLower(a.Name), ".sha256") {
			fallbacks = append(fallbacks, a)
		}
	}
	switch len(fallbacks) {
	case 0:
		return github.Asset{}, fmt.Errorf("checksum: no .sha256 asset in release (looked for %q)", want)
	case 1:
		return fallbacks[0], nil
	default:
		return github.Asset{}, fmt.Errorf("checksum: multiple .sha256 assets but none named %q", want)
	}
}

// verifyChecksum hashes archivePath with SHA-256 and compares against the
// digest located in digestPath. digestPath may be a single hex line
// (the `<asset>.sha256` flavour) or a sha256sum-style listing.
func verifyChecksum(archivePath, assetName, digestPath string) error {
	expected, err := parseDigestFile(digestPath, assetName)
	if err != nil {
		return err
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("checksum: open archive: %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("checksum: read archive: %v", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, expected)
	}
	return nil
}

// parseDigestFile understands two on-disk formats:
//  1. A single 64-char hex digest (with optional surrounding whitespace).
//  2. Lines of `<hex>  <filename>` (sha256sum format; "*<filename>" in
//     binary mode is normalized).
//
// Matching is by basename — combined checksum files often qualify names
// with paths.
func parseDigestFile(path, assetName string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("checksum: read digest: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Single-hex shortcut: only one non-empty line, and it's pure hex.
	nonEmpty := make([]string, 0, len(lines))
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 1 && isHex64(nonEmpty[0]) {
		return nonEmpty[0], nil
	}

	for _, line := range nonEmpty {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if !isHex64(fields[0]) {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filepath.Base(name) == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum: no entry for %s in digest file", assetName)
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
