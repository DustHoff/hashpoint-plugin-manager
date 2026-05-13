package config

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"
)

func TestParse_HappyPath_AllFields(t *testing.T) {
	cfg := sdk.PluginConfig{
		Fields: map[string]string{
			"repo_index_url":      "https://raw.githubusercontent.com/owner/repo/main/repo.json",
			"include_prereleases": "true",
			"pinned_versions":     `{"oncall-jira":"1.2.3","oncall-otrs":"v1.0.0"}`,
			"cache_ttl_seconds":   "60",
		},
		Secrets: map[string]sdk.SecretHandle{
			"github_token": "handle-XYZ",
		},
	}
	got, err := Parse(cfg, 1)
	if err != nil {
		t.Fatalf("Parse: unexpected err: %v", err)
	}
	if got.RepoIndexURL != cfg.Fields["repo_index_url"] {
		t.Errorf("RepoIndexURL = %q, want %q", got.RepoIndexURL, cfg.Fields["repo_index_url"])
	}
	if got.TokenHandle != "handle-XYZ" {
		t.Errorf("TokenHandle = %q, want handle-XYZ", got.TokenHandle)
	}
	if !got.IncludePrereleases {
		t.Errorf("IncludePrereleases = false, want true")
	}
	if got.CacheTTLSeconds != 60 {
		t.Errorf("CacheTTLSeconds = %d, want 60", got.CacheTTLSeconds)
	}
	if got.Pins["oncall-jira"] != "v1.2.3" {
		t.Errorf("pin oncall-jira normalized = %q, want v1.2.3", got.Pins["oncall-jira"])
	}
	if got.Pins["oncall-otrs"] != "v1.0.0" {
		t.Errorf("pin oncall-otrs normalized = %q, want v1.0.0", got.Pins["oncall-otrs"])
	}
}

func TestParse_Defaults(t *testing.T) {
	cfg := sdk.PluginConfig{
		Fields: map[string]string{"repo_index_url": "https://raw.githubusercontent.com/a/b/main/repo.json"},
	}
	got, err := Parse(cfg, 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.CacheTTLSeconds != DefaultCacheTTLSeconds {
		t.Errorf("CacheTTLSeconds default = %d, want %d", got.CacheTTLSeconds, DefaultCacheTTLSeconds)
	}
	if got.IncludePrereleases {
		t.Errorf("IncludePrereleases default = true, want false")
	}
	if got.Pins != nil {
		t.Errorf("Pins default = %v, want nil", got.Pins)
	}
	if got.TokenHandle != "" {
		t.Errorf("TokenHandle default = %q, want empty", got.TokenHandle)
	}
}

func TestParse_RepoIndexURL_MissingFallsBackToDefault(t *testing.T) {
	cases := []sdk.PluginConfig{
		{Fields: map[string]string{}},
		{Fields: map[string]string{"repo_index_url": "   "}},
		{Fields: map[string]string{"repo_index_url": ""}},
	}
	for i, cfg := range cases {
		got, err := Parse(cfg, 1)
		if err != nil {
			t.Fatalf("case %d: unexpected err = %v (default should apply)", i, err)
		}
		if got.RepoIndexURL != DefaultRepoIndexURL {
			t.Errorf("case %d: RepoIndexURL = %q, want DefaultRepoIndexURL %q", i, got.RepoIndexURL, DefaultRepoIndexURL)
		}
	}
}

func TestParse_RepoIndexURL_DefaultIsHTTPS(t *testing.T) {
	if !strings.HasPrefix(DefaultRepoIndexURL, "https://") {
		t.Errorf("DefaultRepoIndexURL = %q, want https:// prefix (§9 HTTPS-only)", DefaultRepoIndexURL)
	}
}

func TestParse_RepoIndexURL_NonHTTPSIsErrConfigInvalid(t *testing.T) {
	for _, bad := range []string{
		"http://example.com/repo.json", // non-HTTPS
		"file:///etc/passwd",
		"://bad",
		"not a url at all",
	} {
		cfg := sdk.PluginConfig{Fields: map[string]string{"repo_index_url": bad}}
		_, err := Parse(cfg, 1)
		if !errors.Is(err, sdk.ErrConfigInvalid) {
			t.Errorf("repo_index_url=%q: err = %v, want wrap of ErrConfigInvalid", bad, err)
		}
	}
}

func TestParse_IncludePrereleases_Invalid(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":      "https://raw.githubusercontent.com/a/b/main/repo.json",
		"include_prereleases": "yesplz",
	}}
	if _, err := Parse(cfg, 1); !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestParse_CacheTTLSeconds_Invalid(t *testing.T) {
	for _, bad := range []string{"abc", "-1", "3.14"} {
		cfg := sdk.PluginConfig{Fields: map[string]string{
			"repo_index_url":    "https://raw.githubusercontent.com/a/b/main/repo.json",
			"cache_ttl_seconds": bad,
		}}
		if _, err := Parse(cfg, 1); !errors.Is(err, sdk.ErrConfigInvalid) {
			t.Errorf("cache_ttl_seconds=%q: err = %v, want wrap of ErrConfigInvalid", bad, err)
		}
	}
}

func TestParse_PinnedVersions_BadJSON(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":  "https://raw.githubusercontent.com/a/b/main/repo.json",
		"pinned_versions": `{"oncall-jira": "1.2.3"`, // missing closing brace
	}}
	if _, err := Parse(cfg, 1); !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestParse_PinnedVersions_InvalidSemver(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":  "https://raw.githubusercontent.com/a/b/main/repo.json",
		"pinned_versions": `{"oncall-jira": "not-a-version"}`,
	}}
	_, err := Parse(cfg, 1)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "valid semver") {
		t.Errorf("err message = %q, want it to mention 'valid semver'", err.Error())
	}
}

func TestParse_PinnedVersions_MajorMismatch(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":  "https://raw.githubusercontent.com/a/b/main/repo.json",
		"pinned_versions": `{"oncall-jira": "2.0.0"}`, // major 2, HostAPI is 1
	}}
	_, err := Parse(cfg, 1)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "Major-Rule") {
		t.Errorf("err message = %q, want it to mention 'Major-Rule'", err.Error())
	}
}

func TestParse_PinnedVersions_EmptyName(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":  "https://raw.githubusercontent.com/a/b/main/repo.json",
		"pinned_versions": `{"  ": "1.0.0"}`,
	}}
	if _, err := Parse(cfg, 1); !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestParse_CacheTTL_Zero_IsAllowed(t *testing.T) {
	cfg := sdk.PluginConfig{Fields: map[string]string{
		"repo_index_url":    "https://raw.githubusercontent.com/a/b/main/repo.json",
		"cache_ttl_seconds": "0",
	}}
	got, err := Parse(cfg, 1)
	if err != nil {
		t.Fatalf("Parse with cache_ttl_seconds=0: %v", err)
	}
	if got.CacheTTLSeconds != 0 {
		t.Errorf("CacheTTLSeconds = %d, want 0 (disabled cache)", got.CacheTTLSeconds)
	}
}

func TestNormalizeSemver(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"  1.0.0  ", "v1.0.0"},
		{"", ""},
		{"v0.0.0-rc.1+meta", "v0.0.0-rc.1+meta"},
	}
	for _, c := range cases {
		if got := NormalizeSemver(c.in); got != c.out {
			t.Errorf("NormalizeSemver(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
