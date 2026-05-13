package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/httpx"
	"github.com/dusthoff/hashpoint-plugin-manager/internal/logging"
)

// allowTestHost adds host to the whitelist for the duration of t.
// Tests in this package run sequentially (no t.Parallel), so the
// shared-map mutation is safe.
func allowTestHost(t *testing.T, host string) {
	t.Helper()
	httpx.AllowedHosts[host] = struct{}{}
	t.Cleanup(func() { delete(httpx.AllowedHosts, host) })
}

func startServer(t *testing.T, h http.Handler) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv.URL: %v", err)
	}
	allowTestHost(t, u.Host)
	return srv.URL, srv.Client()
}

func TestFetchPage_HappyPath_ReturnsReleases(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
		  {"tag_name":"v1.2.3","draft":false,"prerelease":false,"assets":[{"name":"a.zip","browser_download_url":"https://x","size":42}]},
		  {"tag_name":"v1.1.0","draft":false,"prerelease":false,"assets":[]}
		]`))
	}))
	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	rels, next, err := c.fetchPage(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if len(rels) != 2 || rels[0].TagName != "v1.2.3" {
		t.Errorf("rels = %+v, want 2 releases starting v1.2.3", rels)
	}
	if next != "" {
		t.Errorf("next = %q, want empty (no Link header)", next)
	}
}

func TestFetchPage_TokenInjectedAsBearer(t *testing.T) {
	const wantToken = "ghp_TESTTOKEN"
	var sawAuth string
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	c := NewClient(client, logging.Nop{},
		func(context.Context) (string, error) { return wantToken, nil },
		time.Minute)
	_, _, err := c.fetchPage(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if sawAuth != "Bearer "+wantToken {
		t.Errorf("Authorization header = %q, want Bearer %s", sawAuth, wantToken)
	}
}

func TestFetchPage_NoTokenProvider_OmitsAuth(t *testing.T) {
	var sawAuth string
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	if _, _, err := c.fetchPage(context.Background(), url); err != nil {
		t.Fatalf("fetchPage: %v", err)
	}
	if sawAuth != "" {
		t.Errorf("Authorization header = %q, want empty (no token)", sawAuth)
	}
}

func TestFetchPage_403WithRateLimitZero_IsTransient(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	_, _, err := c.fetchPage(context.Background(), url)
	if !errors.Is(err, sdk.ErrTransient) {
		t.Errorf("err = %v, want wrap of ErrTransient", err)
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("err message = %q, want it to mention rate limit", err.Error())
	}
}

func TestFetchPage_5xx_IsTransient(t *testing.T) {
	for _, code := range []int{500, 502, 503, 504} {
		t.Run(fmt.Sprintf("status=%d", code), func(t *testing.T) {
			code := code
			url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			c := NewClient(client, logging.Nop{}, nil, time.Minute)
			_, _, err := c.fetchPage(context.Background(), url)
			if !errors.Is(err, sdk.ErrTransient) {
				t.Errorf("err = %v, want wrap of ErrTransient", err)
			}
		})
	}
}

func TestFetchPage_429_IsTransient(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	_, _, err := c.fetchPage(context.Background(), url)
	if !errors.Is(err, sdk.ErrTransient) {
		t.Errorf("err = %v, want wrap of ErrTransient", err)
	}
}

func TestFetchPage_404_IsBareError(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	_, _, err := c.fetchPage(context.Background(), url)
	if err == nil {
		t.Fatalf("err = nil, want a 404 error")
	}
	if errors.Is(err, sdk.ErrTransient) {
		t.Errorf("404 wrapped as transient: %v", err)
	}
	if errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("404 wrapped as config-invalid: %v", err)
	}
}

func TestResolveToken_PropagatesUnknownSecretHandle(t *testing.T) {
	c := NewClient(http.DefaultClient, logging.Nop{},
		func(context.Context) (string, error) {
			return "", fmt.Errorf("wrapped: %w", sdk.ErrUnknownSecretHandle)
		},
		time.Minute)
	_, err := c.resolveToken(context.Background())
	if !errors.Is(err, sdk.ErrUnknownSecretHandle) {
		t.Errorf("err = %v, want wrap of ErrUnknownSecretHandle", err)
	}
}

func TestResolveToken_GenericErrorIsWrapped(t *testing.T) {
	c := NewClient(http.DefaultClient, logging.Nop{},
		func(context.Context) (string, error) { return "", fmt.Errorf("boom") },
		time.Minute)
	_, err := c.resolveToken(context.Background())
	if err == nil {
		t.Fatalf("err = nil, want non-nil")
	}
	if errors.Is(err, sdk.ErrUnknownSecretHandle) {
		t.Errorf("generic error mistaken for ErrUnknownSecretHandle")
	}
}

func TestParseNextLink(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{`<https://api.github.com/x?page=2>; rel="next"`, "https://api.github.com/x?page=2"},
		{`<https://x/a>; rel="prev", <https://x/b>; rel="next", <https://x/c>; rel="last"`, "https://x/b"},
		{`<https://x>; rel="last"`, ""},
	}
	for _, c := range cases {
		if got := parseNextLink(c.in); got != c.want {
			t.Errorf("parseNextLink(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListReleases_PaginationWalks(t *testing.T) {
	var calls atomic.Int32
	// Two-page handler: first response returns one release + Link rel="next",
	// second response returns one release and no Link header.
	var srvURL string
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s/page2>; rel="next"`, srvURL))
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v1.1.0"}]`))
	}))
	srvURL = url

	c := NewClient(client, logging.Nop{}, nil, time.Minute)
	// Walk fetchPage manually to avoid hardcoding the api.github.com base URL.
	rels1, next, err := c.fetchPage(context.Background(), url)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if next == "" {
		t.Fatalf("page1: no next link returned")
	}
	rels2, next2, err := c.fetchPage(context.Background(), next)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if next2 != "" {
		t.Errorf("page2: next2 = %q, want empty", next2)
	}
	got := append(rels1, rels2...)
	if len(got) != 2 || got[0].TagName != "v1.0.0" || got[1].TagName != "v1.1.0" {
		t.Errorf("paginated releases = %+v, want [v1.0.0, v1.1.0]", got)
	}
}

func TestListReleases_BadOwnerRepo_IsErrConfigInvalid(t *testing.T) {
	c := NewClient(http.DefaultClient, logging.Nop{}, nil, time.Minute)
	_, err := c.ListReleases(context.Background(), "no-slash-here")
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}
