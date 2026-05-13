// Package catalog owns the repo.json index: fetching it over HTTPS,
// validating schema_version, and caching the parsed result. Per spec
// §3.1, there is exactly one repo.json — no multi-source.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/cache"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/httpx"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
)

// SchemaVersion is the only repo.json shape this manager understands.
// Mismatch ⇒ ErrConfigInvalid per spec §3.1.
const SchemaVersion = 1

// maxIndexBytes caps the response body — repo.json is JSON-of-strings,
// 4 MiB is comfortably above realistic sizes.
const maxIndexBytes = 4 << 20

// Index mirrors the v1 repo.json schema (§3.1).
type Index struct {
	SchemaVersion int     `json:"schema_version"`
	Plugins       []Entry `json:"plugins"`
}

// Entry is one row in the catalog. AssetPattern is empty when the
// publisher accepts the default `{name}_{version}_{os}_{arch}.zip`.
type Entry struct {
	Name         string   `json:"name"`
	Repository   string   `json:"repository"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	AssetPattern string   `json:"asset_pattern,omitempty"`
}

// FindByName returns the catalog entry for the named plugin, or false.
// Always check the bool — the zero Entry will silently fail downstream.
func (i *Index) FindByName(name string) (Entry, bool) {
	for _, e := range i.Plugins {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Client fetches and caches the repo.json index. One Client per Plugin
// instance; Configure() rebuilds it with the freshly-parsed TTL.
type Client struct {
	http  *http.Client
	log   logging.Logger
	cache *cache.TTL[*Index]
}

// NewClient wires the shared http.Client and a TTL cache keyed by URL
// (a single Client may be queried for multiple URLs across the
// instance's lifetime, though in practice the URL is config-stable).
func NewClient(httpClient *http.Client, log logging.Logger, ttl time.Duration) *Client {
	return &Client{
		http:  httpClient,
		log:   log,
		cache: cache.New[*Index](ttl),
	}
}

// ResetCache drops every cached index. Called by Configure().
func (c *Client) ResetCache() { c.cache.Reset() }

// Load returns the parsed index, honoring the TTL cache. Errors are
// wrapped sentinels: transport/5xx ⇒ ErrTransient, schema/JSON failure
// ⇒ ErrConfigInvalid, non-2xx other ⇒ bare error.
func (c *Client) Load(ctx context.Context, indexURL string) (*Index, error) {
	if v, ok := c.cache.Get(indexURL); ok {
		c.log.Debug(ctx, "catalog cache hit", map[string]string{"url": indexURL})
		return v, nil
	}
	c.log.Debug(ctx, "catalog cache miss", map[string]string{"url": indexURL})

	if err := httpx.AssertWhitelisted(indexURL); err != nil {
		return nil, fmt.Errorf("%w: %v", sdk.ErrConfigInvalid, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", sdk.ErrConfigInvalid, err)
	}
	req.Header.Set("Accept", "application/json, text/plain")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch repo.json: %v", sdk.ErrTransient, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: repo.json fetch returned %s", sdk.ErrTransient, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: repo.json not found (check repo_index_url)", sdk.ErrConfigInvalid)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("repo.json fetch returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read repo.json body: %v", sdk.ErrTransient, err)
	}
	if int64(len(body)) > maxIndexBytes {
		return nil, fmt.Errorf("%w: repo.json exceeds %d bytes", sdk.ErrConfigInvalid, maxIndexBytes)
	}

	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("%w: parse repo.json: %v", sdk.ErrConfigInvalid, err)
	}
	if idx.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: repo.json schema_version %d unsupported (expected %d)", sdk.ErrConfigInvalid, idx.SchemaVersion, SchemaVersion)
	}
	for i, e := range idx.Plugins {
		if strings.TrimSpace(e.Name) == "" {
			return nil, fmt.Errorf("%w: repo.json plugins[%d].name missing", sdk.ErrConfigInvalid, i)
		}
		if !validOwnerRepo(e.Repository) {
			return nil, fmt.Errorf("%w: repo.json plugins[%d].repository invalid: %q", sdk.ErrConfigInvalid, i, e.Repository)
		}
	}

	c.cache.Set(indexURL, &idx)
	return &idx, nil
}

// validOwnerRepo checks the "owner/repo" shape with non-empty halves.
func validOwnerRepo(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	return parts[0] != "" && parts[1] != ""
}
