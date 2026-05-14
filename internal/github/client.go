// Package github implements the minimal GitHub Releases client this
// manager needs: list releases for owner/repo, with optional PAT auth.
// Caching, filtering (§4) and asset resolution live in sibling files.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/cache"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
)

// Release mirrors the subset of /repos/{owner}/{repo}/releases we use.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name,omitempty"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset is one file attached to a Release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// TokenProvider yields a GitHub PAT for outbound auth. Implementations
// typically wrap sdk.HostAPI.RedeemSecret. An empty string means "no
// auth" — the request goes out unauthenticated and is subject to the
// 60 req/h anonymous rate limit.
type TokenProvider func(ctx context.Context) (string, error)

// MaxPages caps pagination. We ask for per_page=100, so 5 pages = 500
// releases — more than any sane plugin repo will publish.
const MaxPages = 5

// maxBodyBytes caps a single release-list page; the body is JSON.
const maxBodyBytes = 16 << 20

// Client lists GitHub releases. One Client per Plugin instance; rebuilt
// on Configure() with a fresh cache and (possibly) token provider.
type Client struct {
	http  *http.Client
	log   logging.Logger
	token TokenProvider
	cache *cache.TTL[[]Release]
}

// NewClient wires the shared http.Client and TTL cache. tokenProv may
// be nil — pass nil for anonymous access.
func NewClient(httpClient *http.Client, log logging.Logger, tokenProv TokenProvider, ttl time.Duration) *Client {
	return &Client{
		http:  httpClient,
		log:   log,
		token: tokenProv,
		cache: cache.New[[]Release](ttl),
	}
}

// ResetCache drops every cached release list.
func (c *Client) ResetCache() { c.cache.Reset() }

// ListReleases returns every (page-walked) release for "owner/repo".
// Cache key is the bare owner/repo string. Errors are wrapped:
// rate-limit / 5xx ⇒ ErrTransient; auth-secret stale ⇒
// ErrUnknownSecretHandle (surfaced as-is so the host re-prompts).
func (c *Client) ListReleases(ctx context.Context, ownerRepo string) ([]Release, error) {
	if v, ok := c.cache.Get(ownerRepo); ok {
		c.log.Debug(ctx, "github cache hit", map[string]string{"repo": ownerRepo})
		return v, nil
	}
	c.log.Debug(ctx, "github cache miss", map[string]string{"repo": ownerRepo})

	parts := strings.Split(ownerRepo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("%w: invalid owner/repo %q", sdk.ErrConfigInvalid, ownerRepo)
	}
	pageURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100", parts[0], parts[1])

	var all []Release
	for page := 0; page < MaxPages; page++ {
		rels, next, err := c.fetchPage(ctx, pageURL)
		if err != nil {
			return nil, err
		}
		all = append(all, rels...)
		if next == "" {
			break
		}
		pageURL = next
	}
	c.cache.Set(ownerRepo, all)
	return all, nil
}

func (c *Client) fetchPage(ctx context.Context, pageURL string) ([]Release, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	token, err := c.resolveToken(ctx)
	if err != nil {
		return nil, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: github releases: %v", sdk.ErrTransient, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, "", fmt.Errorf("%w: github rate limit exhausted", sdk.ErrTransient)
	case resp.StatusCode == http.StatusForbidden:
		return nil, "", fmt.Errorf("%w: github 403: %s", sdk.ErrTransient, resp.Status)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, "", fmt.Errorf("%w: github 429", sdk.ErrTransient)
	case resp.StatusCode >= 500:
		return nil, "", fmt.Errorf("%w: github %s", sdk.ErrTransient, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", fmt.Errorf("github repo not found")
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("github returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read github body: %v", sdk.ErrTransient, err)
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, "", fmt.Errorf("parse github releases: %v", err)
	}
	return releases, parseNextLink(resp.Header.Get("Link")), nil
}

// resolveToken redeems the PAT handle if one is configured. Stale
// handles return wrapped ErrUnknownSecretHandle so callers can detect
// the user-action-required case via errors.Is.
func (c *Client) resolveToken(ctx context.Context) (string, error) {
	if c.token == nil {
		return "", nil
	}
	t, err := c.token(ctx)
	if err != nil {
		if errors.Is(err, sdk.ErrUnknownSecretHandle) {
			return "", err
		}
		return "", fmt.Errorf("redeem github token: %v", err)
	}
	return t, nil
}

// parseNextLink extracts the rel="next" URL from a GitHub Link header.
// Returns "" when the page is the last one.
func parseNextLink(h string) string {
	if h == "" {
		return ""
	}
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		lt := strings.IndexByte(part, '<')
		gt := strings.IndexByte(part, '>')
		if lt >= 0 && gt > lt {
			return part[lt+1 : gt]
		}
	}
	return ""
}
