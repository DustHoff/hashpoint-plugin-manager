// Package plugin is the sdk.Plugin + sdk.PluginManagementHandler
// implementation. Everything substantive lives in sibling internal
// packages (config, catalog, github, installer); this file is glue:
//   - lifecycle methods (Init / Metadata / Configure)
//   - the four handler methods (ListAvailable / Install / Update / Uninstall)
//   - the self-reference + self-check guards
package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"
	"golang.org/x/mod/semver"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/catalog"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/config"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/github"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/httpx"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/installer"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/version"
)

// listAvailableConcurrency bounds how many GitHub repos we hit in
// parallel inside ListAvailable. Spec §6.1 calls for a "begrenzte
// Concurrency, z. B. 8".
const listAvailableConcurrency = 8

// Plugin holds the runtime state. Init fills the host/log/pluginsDir/
// httpc fields; Configure fills/replaces the remaining ones under the
// mutex. Handler methods read under the same mutex and otherwise hold
// no state — they construct a per-call Installer instance.
type Plugin struct {
	mu sync.RWMutex

	host       sdk.HostAPI
	log        logging.Logger
	httpc      *http.Client
	pluginsDir string

	cfg     *config.Config
	catalog *catalog.Client
	github  *github.Client
}

// New returns a fresh Plugin instance, suitable for sdk.Serve.
func New() *Plugin { return &Plugin{} }

// ---- sdk.Plugin lifecycle ----

// Init runs the spec §2.2 self-check, derives PluginsDir from the
// running executable's location, and builds the shared HTTP client.
// No network I/O happens here.
func (p *Plugin) Init(_ context.Context, host sdk.HostAPI) error {
	p.host = host
	p.log = logging.FromHost(host)

	own := config.NormalizeSemver(version.Manager)
	if !semver.IsValid(own) {
		return fmt.Errorf("%w: manager version %q is not valid semver", sdk.ErrConfigInvalid, version.Manager)
	}
	want := fmt.Sprintf("v%d", sdk.HostAPIVersion)
	if semver.Major(own) != want {
		return fmt.Errorf("%w: manager major %s ≠ sdk.HostAPIVersion %d",
			sdk.ErrConfigInvalid, semver.Major(own), sdk.HostAPIVersion)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("%w: locate own executable: %v", sdk.ErrConfigInvalid, err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Plugin binary lives at <PluginsDir>/<name>/<name>(.exe), so the
	// dir two levels up is PluginsDir.
	p.pluginsDir = filepath.Dir(filepath.Dir(exe))

	p.httpc = httpx.NewClient(0) // context deadlines drive timeouts
	return nil
}

// Metadata is the static manifest mirror returned to the host. Must stay
// in sync with manifest.toml.
func (p *Plugin) Metadata(_ context.Context) (sdk.Metadata, error) {
	return sdk.Metadata{
		Name:         version.Name,
		Version:      version.Manager,
		APIVersion:   sdk.HostAPIVersion,
		Capabilities: []sdk.Capability{sdk.CapPluginManagement},
		Description:  "Installs and updates Hashpoint plugins from a GitHub-based repo index.",
	}, nil
}

// Configure parses the host-delivered settings, busts caches, and
// rebuilds the catalog/github clients with the freshly-resolved TTL
// and token provider.
func (p *Plugin) Configure(ctx context.Context, cfg sdk.PluginConfig) error {
	parsed, err := config.Parse(cfg, sdk.HostAPIVersion)
	if err != nil {
		return err
	}

	ttl := time.Duration(parsed.CacheTTLSeconds) * time.Second

	p.mu.Lock()
	defer p.mu.Unlock()

	host := p.host
	p.cfg = parsed
	p.catalog = catalog.NewClient(p.httpc, p.log, ttl)

	var tokenProv github.TokenProvider
	if parsed.TokenHandle != "" {
		handle := parsed.TokenHandle
		tokenProv = func(ctx context.Context) (string, error) {
			return host.RedeemSecret(ctx, handle)
		}
	}
	p.github = github.NewClient(p.httpc, p.log, tokenProv, ttl)

	p.log.Info(ctx, "plugin-manager configured", map[string]string{
		"repo_index_url":      httpx.RedactURL(parsed.RepoIndexURL),
		"include_prereleases": fmt.Sprintf("%t", parsed.IncludePrereleases),
		"cache_ttl_seconds":   fmt.Sprintf("%d", parsed.CacheTTLSeconds),
		"pinned_count":        fmt.Sprintf("%d", len(parsed.Pins)),
	})
	return nil
}

// snapshot returns the current config + clients under the read lock.
// Returns ErrNotConfigured when Configure has not yet been called.
func (p *Plugin) snapshot() (*config.Config, *catalog.Client, *github.Client, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cfg == nil || p.catalog == nil || p.github == nil {
		return nil, nil, nil, fmt.Errorf("%w: Configure has not been called", sdk.ErrNotConfigured)
	}
	return p.cfg, p.catalog, p.github, nil
}

// ---- sdk.PluginManagementHandler ----

// ListAvailable resolves the catalog into displayable AvailablePlugin
// entries. Per-plugin errors that aren't auth-related are logged and
// skipped — a single repo being unreachable shouldn't blank the whole
// list. ErrUnknownSecretHandle propagates as-is so the host can
// re-prompt the user for a fresh PAT.
func (p *Plugin) ListAvailable(ctx context.Context) ([]sdk.AvailablePlugin, error) {
	cfg, cat, gh, err := p.snapshot()
	if err != nil {
		return nil, err
	}
	idx, err := cat.Load(ctx, cfg.RepoIndexURL)
	if err != nil {
		return nil, err
	}

	type result struct {
		ap  *sdk.AvailablePlugin
		err error
	}
	resCh := make(chan result, len(idx.Plugins))
	sem := make(chan struct{}, listAvailableConcurrency)
	var wg sync.WaitGroup

	for _, e := range idx.Plugins {
		// The manager never advertises itself in its own catalog — even
		// if a repo.json publisher lists us, suppress the row so the UI
		// can't offer a self-install button.
		if e.Name == version.Name {
			continue
		}
		entry := e
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ap, err := p.resolveOne(ctx, cfg, gh, entry)
			resCh <- result{ap: ap, err: err}
		}()
	}
	go func() { wg.Wait(); close(resCh) }()

	out := make([]sdk.AvailablePlugin, 0, len(idx.Plugins))
	var fatal error
	for r := range resCh {
		if r.err != nil {
			if errors.Is(r.err, sdk.ErrUnknownSecretHandle) && fatal == nil {
				fatal = r.err
			}
			continue
		}
		if r.ap != nil {
			out = append(out, *r.ap)
		}
	}
	if fatal != nil {
		return nil, fatal
	}
	return out, nil
}

// resolveOne returns the AvailablePlugin row for one catalog entry, or
// (nil, nil) when the plugin should be silently skipped (no compatible
// release, pin unreachable, transient repo error). Auth-stale errors are
// returned so the caller can propagate them as ErrUnknownSecretHandle.
func (p *Plugin) resolveOne(ctx context.Context, cfg *config.Config, gh *github.Client, e catalog.Entry) (*sdk.AvailablePlugin, error) {
	releases, err := gh.ListReleases(ctx, e.Repository)
	if err != nil {
		if errors.Is(err, sdk.ErrUnknownSecretHandle) {
			return nil, err
		}
		p.log.Warn(ctx, "github releases fetch failed", map[string]string{
			"name": e.Name, "repo": e.Repository, "err": err.Error(),
		})
		return nil, nil
	}

	candidates := github.FilterCandidates(releases, github.CandidateOpts{
		IncludePrereleases: cfg.IncludePrereleases,
		HostAPIMajor:       sdk.HostAPIVersion,
	})
	if len(candidates) == 0 {
		p.log.Debug(ctx, "no compatible release for plugin", map[string]string{
			"name":               e.Name,
			"sdk.HostAPIVersion": fmt.Sprintf("%d", sdk.HostAPIVersion),
		})
		return nil, nil
	}

	target := candidates[0]
	if pin, pinned := cfg.Pins[e.Name]; pinned {
		r, ok := github.FindByVersion(candidates, pin)
		if !ok {
			p.log.Warn(ctx, "pinned version not in candidate set", map[string]string{
				"name": e.Name, "pin": pin,
			})
			return nil, nil
		}
		target = r
	}

	return &sdk.AvailablePlugin{
		Name:        e.Name,
		Version:     strings.TrimPrefix(target.TagName, "v"),
		Description: e.Description,
	}, nil
}

// Install fetches the chosen release for name and places it under
// <PluginsDir>/<name>/. Errors are surfaced verbatim to the UI per the
// SDK contract; sentinel-wrapped errors keep their classification.
func (p *Plugin) Install(ctx context.Context, name string) error {
	return p.installOrUpdate(ctx, name, false)
}

// Update is like Install but expects an existing directory and supports
// rollback via .old swap.
func (p *Plugin) Update(ctx context.Context, name string) error {
	return p.installOrUpdate(ctx, name, true)
}

func (p *Plugin) installOrUpdate(ctx context.Context, name string, isUpdate bool) error {
	if name == version.Name {
		return fmt.Errorf("self-update not supported")
	}

	cfg, cat, gh, err := p.snapshot()
	if err != nil {
		return err
	}
	idx, err := cat.Load(ctx, cfg.RepoIndexURL)
	if err != nil {
		return err
	}
	entry, ok := idx.FindByName(name)
	if !ok {
		return fmt.Errorf("%w: plugin %q not in repo.json", sdk.ErrConfigInvalid, name)
	}
	releases, err := gh.ListReleases(ctx, entry.Repository)
	if err != nil {
		return err
	}
	candidates := github.FilterCandidates(releases, github.CandidateOpts{
		IncludePrereleases: cfg.IncludePrereleases,
		HostAPIMajor:       sdk.HostAPIVersion,
	})
	if len(candidates) == 0 {
		return fmt.Errorf("%w: no compatible release for %q (sdk.HostAPIVersion=%d)",
			sdk.ErrConfigInvalid, name, sdk.HostAPIVersion)
	}
	target := candidates[0]
	if pin, pinned := cfg.Pins[name]; pinned {
		r, ok := github.FindByVersion(candidates, pin)
		if !ok {
			return fmt.Errorf("%w: pinned version %s for %q not available as compatible release",
				sdk.ErrConfigInvalid, pin, name)
		}
		target = r
	}

	// Spec §6.2 step 4: defensive Major-Rule recheck. FilterCandidates
	// already enforces this, but a regression there shouldn't slip past
	// the install path.
	wantMajor := fmt.Sprintf("v%d", sdk.HostAPIVersion)
	if semver.Major(target.TagName) != wantMajor {
		return fmt.Errorf("%w: chosen release %s for %q violates Major-Rule (want %s)",
			sdk.ErrConfigInvalid, target.TagName, name, wantMajor)
	}

	p.mu.RLock()
	httpc, log, pluginsDir := p.httpc, p.log, p.pluginsDir
	p.mu.RUnlock()

	in := &installer.Installer{
		PluginsDir:   pluginsDir,
		HTTPClient:   httpc,
		Log:          log,
		HostAPIMajor: sdk.HostAPIVersion,
	}
	plan := installer.Plan{Name: name, Entry: entry, Release: target}

	if isUpdate {
		log.Info(ctx, "plugin update starting", map[string]string{"name": name, "version": target.TagName})
		return in.Update(ctx, plan)
	}
	log.Info(ctx, "plugin install starting", map[string]string{"name": name, "version": target.TagName})
	return in.Install(ctx, plan)
}

// Uninstall removes <PluginsDir>/<name>/. The host already refuses
// self-uninstall before it reaches us, but we defend in depth.
func (p *Plugin) Uninstall(ctx context.Context, name string) error {
	if name == version.Name {
		return fmt.Errorf("self-uninstall not supported")
	}
	p.mu.RLock()
	log, pluginsDir := p.log, p.pluginsDir
	p.mu.RUnlock()
	if log == nil || pluginsDir == "" {
		return fmt.Errorf("%w: Init has not been called", sdk.ErrNotConfigured)
	}
	in := &installer.Installer{PluginsDir: pluginsDir, Log: log}
	return in.Uninstall(ctx, name)
}
