// Package config parses and validates the per-instance plugin settings.
// All errors leave this package as wrapped sdk sentinels — Parse is the
// single source of truth for the classification rules in spec §7.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"
	"golang.org/x/mod/semver"
)

// Config is the parsed, validated configuration. Build via Parse — never
// construct directly. The zero value is intentionally not useful.
type Config struct {
	RepoIndexURL       string
	TokenHandle        sdk.SecretHandle // empty when no PAT provided
	IncludePrereleases bool
	Pins               map[string]string // plugin name -> "vX.Y.Z[…]" (normalized)
	CacheTTLSeconds    int
}

// Manifest field keys. Kept private so the rest of the codebase touches
// them only through the typed Config.
const (
	fieldRepoIndexURL       = "repo_index_url"
	fieldGitHubToken        = "github_token"
	fieldIncludePrereleases = "include_prereleases"
	fieldPinnedVersions     = "pinned_versions"
	fieldCacheTTLSeconds    = "cache_ttl_seconds"
)

// DefaultCacheTTLSeconds matches the manifest default and §8.
const DefaultCacheTTLSeconds = 300

// DefaultRepoIndexURL is the fallback when the user leaves repo_index_url
// blank. It points at the repo.json shipped on this repository's main
// branch — see spec §3.1. Override per release by recompiling; runtime
// override happens through the user-supplied config field.
const DefaultRepoIndexURL = "https://raw.githubusercontent.com/DustHoff/hashpoint-plugin-manager/main/repo.json"

// Parse turns the host-delivered PluginConfig into a typed Config.
// hostAPIMajor is the int value of sdk.HostAPIVersion at the call site —
// passed in rather than imported here to keep this package easy to test.
func Parse(cfg sdk.PluginConfig, hostAPIMajor int) (*Config, error) {
	out := &Config{CacheTTLSeconds: DefaultCacheTTLSeconds}

	// repo_index_url — optional. Empty ⇒ fall back to DefaultRepoIndexURL
	// (the canonical catalog on this repo's main branch). A non-empty but
	// malformed value is still ErrConfigInvalid: a user who wrote something
	// meant to override the default, so silently swallowing the bad input
	// would mask the typo.
	raw := strings.TrimSpace(cfg.Fields[fieldRepoIndexURL])
	if raw == "" {
		out.RepoIndexURL = DefaultRepoIndexURL
	} else {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, fmt.Errorf("%w: %s must be an https URL", sdk.ErrConfigInvalid, fieldRepoIndexURL)
		}
		out.RepoIndexURL = raw
	}

	if h, ok := cfg.Secrets[fieldGitHubToken]; ok {
		out.TokenHandle = h
	}

	if v, ok := cfg.Fields[fieldIncludePrereleases]; ok && strings.TrimSpace(v) != "" {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%w: %s must be true/false", sdk.ErrConfigInvalid, fieldIncludePrereleases)
		}
		out.IncludePrereleases = b
	}

	if v, ok := cfg.Fields[fieldCacheTTLSeconds]; ok && strings.TrimSpace(v) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: %s must be a non-negative integer (seconds)", sdk.ErrConfigInvalid, fieldCacheTTLSeconds)
		}
		out.CacheTTLSeconds = n
	}

	if rawPins := strings.TrimSpace(cfg.Fields[fieldPinnedVersions]); rawPins != "" {
		pins, err := parsePins(rawPins, hostAPIMajor)
		if err != nil {
			return nil, err
		}
		out.Pins = pins
	}

	return out, nil
}

// parsePins enforces both shape (JSON object of name→version) and the
// §2 Major-Rule. The §4.1 "pin must exist as a release" check is
// deliberately not done here — it requires network I/O and happens
// lazily inside ListAvailable/Install/Update.
func parsePins(raw string, hostAPIMajor int) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("%w: %s is not valid JSON: %v", sdk.ErrConfigInvalid, fieldPinnedVersions, err)
	}
	wantMajor := fmt.Sprintf("v%d", hostAPIMajor)
	out := make(map[string]string, len(m))
	for name, v := range m {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%w: %s contains empty plugin name", sdk.ErrConfigInvalid, fieldPinnedVersions)
		}
		norm := NormalizeSemver(v)
		if !semver.IsValid(norm) {
			return nil, fmt.Errorf("%w: %s pin for %q is not a valid semver: %q", sdk.ErrConfigInvalid, fieldPinnedVersions, name, v)
		}
		if semver.Major(norm) != wantMajor {
			return nil, fmt.Errorf("%w: %s pin for %q (%s) violates Major-Rule: major must be %d", sdk.ErrConfigInvalid, fieldPinnedVersions, name, v, hostAPIMajor)
		}
		out[name] = norm
	}
	return out, nil
}

// NormalizeSemver ensures the leading "v" prefix expected by
// golang.org/x/mod/semver. Empty input is preserved as empty.
func NormalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
