// Package installer performs the file-on-disk operations Install/Update/
// Uninstall require. Concurrency: callers serialise per plugin name —
// the host arranges that today. Operations on different names are safe
// to interleave because each one stages into its own temp dir.
package installer

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/catalog"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/github"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
)

// DefaultAssetPattern matches the GoReleaser default and spec decision 2.
const DefaultAssetPattern = "{name}_{version}_{os}_{arch}.zip"

// DefaultMaxAssetSize bytes — spec §9 default of 100 MiB.
const DefaultMaxAssetSize int64 = 100 << 20

// Installer wires the shared HTTP client, logger, and policy values used
// by Install / Update / Uninstall. Zero MaxAssetSize means "use default".
type Installer struct {
	PluginsDir   string
	HTTPClient   *http.Client
	Log          logging.Logger
	HostAPIMajor int
	MaxAssetSize int64
}

// Plan is everything fetchAndStage needs to fetch the right bundle for
// one Install/Update. Built by the plugin layer after catalog lookup +
// release resolution.
type Plan struct {
	Name    string
	Entry   catalog.Entry
	Release github.Release
}

// Install fetches plan, verifies it, and places it under
// <PluginsDir>/<name>/. Errors out if the target already exists.
func (in *Installer) Install(ctx context.Context, plan Plan) error {
	target := filepath.Join(in.PluginsDir, plan.Name)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("install %s: %s already exists", plan.Name, target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("install %s: stat target: %v", plan.Name, err)
	}
	staged, err := in.fetchAndStage(ctx, plan)
	if err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("install %s: place: %v", plan.Name, err)
	}
	in.Log.Info(ctx, "plugin installed", map[string]string{"name": plan.Name, "version": plan.Release.TagName})
	return nil
}

// Update replaces an existing <PluginsDir>/<name>/. Old contents are
// swapped to <name>.old first; on failure the swap is reversed.
// The host stops the target subprocess before calling Update, so on
// Windows the old binary is no longer locked.
func (in *Installer) Update(ctx context.Context, plan Plan) error {
	target := filepath.Join(in.PluginsDir, plan.Name)
	if st, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("update %s: not installed", plan.Name)
		}
		return fmt.Errorf("update %s: stat target: %v", plan.Name, err)
	} else if !st.IsDir() {
		return fmt.Errorf("update %s: %s is not a directory", plan.Name, target)
	}

	staged, err := in.fetchAndStage(ctx, plan)
	if err != nil {
		return err
	}

	backup := target + ".old"
	_ = os.RemoveAll(backup) // best-effort: clean up after a prior interrupted update
	if err := os.Rename(target, backup); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("update %s: backup: %v", plan.Name, err)
	}
	if err := os.Rename(staged, target); err != nil {
		if rbErr := os.Rename(backup, target); rbErr != nil {
			in.Log.Error(ctx, "update rollback failed", map[string]string{"name": plan.Name, "err": rbErr.Error()})
		}
		_ = os.RemoveAll(staged)
		return fmt.Errorf("update %s: place: %v", plan.Name, err)
	}
	if err := os.RemoveAll(backup); err != nil {
		in.Log.Warn(ctx, "update: failed to remove backup", map[string]string{"name": plan.Name, "err": err.Error()})
	}
	in.Log.Info(ctx, "plugin updated", map[string]string{"name": plan.Name, "version": plan.Release.TagName})
	return nil
}

// Uninstall removes <PluginsDir>/<name>/. Missing directory is treated as
// success per spec §6.3.
func (in *Installer) Uninstall(ctx context.Context, name string) error {
	target := filepath.Join(in.PluginsDir, name)
	st, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			in.Log.Info(ctx, "plugin already absent", map[string]string{"name": name})
			return nil
		}
		return fmt.Errorf("uninstall %s: stat: %v", name, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("uninstall %s: %s is not a directory", name, target)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("uninstall %s: remove: %v", name, err)
	}
	in.Log.Info(ctx, "plugin uninstalled", map[string]string{"name": name})
	return nil
}

// fetchAndStage handles everything from "resolve asset" through "directory
// ready to rename into PluginsDir". The staged directory is created on
// the same filesystem as PluginsDir so the caller's final os.Rename is
// atomic. Returns the staged directory path; caller owns cleanup on
// failure paths beyond fetchAndStage's control.
func (in *Installer) fetchAndStage(ctx context.Context, plan Plan) (string, error) {
	maxSize := in.MaxAssetSize
	if maxSize == 0 {
		maxSize = DefaultMaxAssetSize
	}

	pattern := plan.Entry.AssetPattern
	if pattern == "" {
		pattern = DefaultAssetPattern
	}
	bareVersion := strings.TrimPrefix(plan.Release.TagName, "v")
	assetName := renderPattern(pattern, plan.Name, bareVersion, runtime.GOOS, runtime.GOARCH)

	asset, ok := findAsset(plan.Release.Assets, assetName)
	if !ok {
		return "", fmt.Errorf("asset %q not found in release %s", assetName, plan.Release.TagName)
	}
	checksum, err := findChecksumAsset(plan.Release.Assets, assetName)
	if err != nil {
		return "", err
	}

	workDir, err := os.MkdirTemp(in.PluginsDir, "."+plan.Name+".stage-")
	if err != nil {
		return "", fmt.Errorf("mktemp under %s: %v", in.PluginsDir, err)
	}
	cleanupWork := func() { _ = os.RemoveAll(workDir) }

	archivePath := filepath.Join(workDir, asset.Name)
	if err := download(ctx, in.HTTPClient, asset.BrowserDownloadURL, archivePath, maxSize); err != nil {
		cleanupWork()
		return "", err
	}
	digestPath := filepath.Join(workDir, checksum.Name)
	if err := download(ctx, in.HTTPClient, checksum.BrowserDownloadURL, digestPath, 1<<20); err != nil {
		cleanupWork()
		return "", err
	}
	if err := verifyChecksum(archivePath, asset.Name, digestPath); err != nil {
		cleanupWork()
		return "", err
	}

	extractDir := filepath.Join(workDir, "extracted")
	if err := unzipSafe(archivePath, extractDir); err != nil {
		cleanupWork()
		return "", err
	}
	if err := assertTopLevel(extractDir, plan.Name); err != nil {
		cleanupWork()
		return "", err
	}
	pluginRoot := filepath.Join(extractDir, plan.Name)
	if err := crossCheckManifest(pluginRoot, plan.Name, in.HostAPIMajor); err != nil {
		cleanupWork()
		return "", err
	}

	// Move the inner <name>/ out of workDir so we can drop the staging
	// debris (archive, checksum, extracted/) and hand back exactly one
	// path. The new path lives next to workDir, still under PluginsDir,
	// so the final os.Rename to <PluginsDir>/<name> stays intra-FS.
	staged := workDir + ".ready"
	if err := os.Rename(pluginRoot, staged); err != nil {
		cleanupWork()
		return "", fmt.Errorf("stage move: %v", err)
	}
	cleanupWork()
	return staged, nil
}

// assertTopLevel verifies the ZIP's only top-level entry is exactly the
// expected plugin directory. Spec §6.2 step 9.
func assertTopLevel(extractDir, name string) error {
	ents, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("read extracted: %v", err)
	}
	if len(ents) != 1 {
		return fmt.Errorf("bundle: expected exactly one top-level entry, got %d", len(ents))
	}
	if ents[0].Name() != name || !ents[0].IsDir() {
		return fmt.Errorf("bundle: top-level must be %s/ directory, got %s", name, ents[0].Name())
	}
	return nil
}

// renderPattern substitutes the four placeholders in the asset pattern.
// Unknown placeholders pass through unchanged so the missing-asset
// error surfaces them clearly.
func renderPattern(pattern, name, version, goos, goarch string) string {
	r := strings.NewReplacer(
		"{name}", name,
		"{version}", version,
		"{os}", goos,
		"{arch}", goarch,
	)
	return r.Replace(pattern)
}

func findAsset(assets []github.Asset, name string) (github.Asset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return github.Asset{}, false
}
