package installer

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// unzipSafe extracts zipPath into destDir with Zip-Slip protection: every
// entry's resolved absolute path must stay inside destDir. Symlink
// entries are refused — plugin bundles are flat-file payloads, anything
// fancier is suspicious.
func unzipSafe(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("zip open: %v", err)
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("zip mkdir dest: %v", err)
	}
	rootAbs, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		if err := extractOne(f, rootAbs); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, rootAbs string) error {
	if f.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("zip: symlink entries not allowed: %q", f.Name)
	}

	cleaned := filepath.Clean(f.Name)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("zip-slip: entry escapes root: %q", f.Name)
	}

	target := filepath.Join(rootAbs, cleaned)
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return fmt.Errorf("zip-slip: entry %q resolves outside root", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, f.Mode().Perm()|0o700)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("zip mkdir parent of %s: %v", f.Name, err)
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("zip open entry %s: %v", f.Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm()|0o600)
	if err != nil {
		return fmt.Errorf("zip create %s: %v", target, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("zip extract %s: %v", f.Name, err)
	}
	return nil
}
