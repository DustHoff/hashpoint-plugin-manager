package catalog

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
// Mutation is safe because tests in this package don't run in parallel.
func allowTestHost(t *testing.T, host string) {
	t.Helper()
	httpx.AllowedHosts[host] = struct{}{}
	t.Cleanup(func() { delete(httpx.AllowedHosts, host) })
}

// startServer builds a TLS httptest server, adds its host to the
// whitelist, and returns the URL plus an *http.Client that trusts
// the test cert.
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

func TestLoad_HappyPath(t *testing.T) {
	body := `{
	  "schema_version": 1,
	  "plugins": [
	    {"name":"oncall-jira","repository":"owner/repo","description":"Files Jira tickets","capabilities":["oncall_documentation"]},
	    {"name":"oncall-otrs","repository":"acme/otrs","asset_pattern":"{name}-{version}-{os}-{arch}.zip"}
	  ]
	}`
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	idx, err := c.Load(context.Background(), url)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", idx.SchemaVersion)
	}
	if len(idx.Plugins) != 2 {
		t.Fatalf("len(Plugins) = %d, want 2", len(idx.Plugins))
	}
	if e, ok := idx.FindByName("oncall-jira"); !ok || e.Repository != "owner/repo" {
		t.Errorf("FindByName(oncall-jira) = %+v ok=%t, want repository owner/repo", e, ok)
	}
	if e, ok := idx.FindByName("oncall-otrs"); !ok || e.AssetPattern != "{name}-{version}-{os}-{arch}.zip" {
		t.Errorf("FindByName(oncall-otrs): asset_pattern = %q, want custom", e.AssetPattern)
	}
}

func TestLoad_SchemaVersionMismatch_ErrConfigInvalid(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema_version": 2, "plugins": []}`))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestLoad_MalformedJSON_ErrConfigInvalid(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema_version": 1, "plugins": [`))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestLoad_404_ErrConfigInvalid(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
}

func TestLoad_5xx_ErrTransient(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrTransient) {
		t.Errorf("err = %v, want wrap of ErrTransient", err)
	}
}

func TestLoad_429_ErrTransient(t *testing.T) {
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrTransient) {
		t.Errorf("err = %v, want wrap of ErrTransient", err)
	}
}

func TestLoad_OversizedBody_ErrConfigInvalid(t *testing.T) {
	// Build a body larger than maxIndexBytes (4 MiB).
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema_version":1,"plugins":[`))
		// emit 5 MiB of comma-separated repeating entries
		entry := []byte(`{"name":"x","repository":"a/b"},`)
		for written := 0; written < 5<<20; written += len(entry) {
			_, _ = w.Write(entry)
		}
		_, _ = w.Write([]byte(`{"name":"x","repository":"a/b"}]}`))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), url)
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid (oversize)", err)
	}
}

func TestLoad_PluginEntryValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing name", `{"schema_version":1,"plugins":[{"name":"  ","repository":"a/b"}]}`},
		{"bad repository (no slash)", `{"schema_version":1,"plugins":[{"name":"x","repository":"justone"}]}`},
		{"bad repository (empty half)", `{"schema_version":1,"plugins":[{"name":"x","repository":"a/"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			cli := NewClient(client, logging.Nop{}, time.Minute)
			_, err := cli.Load(context.Background(), url)
			if !errors.Is(err, sdk.ErrConfigInvalid) {
				t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
			}
		})
	}
}

func TestLoad_CacheHit_OnlyOneFetch(t *testing.T) {
	var calls atomic.Int32
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"schema_version":1,"plugins":[{"name":"x","repository":"a/b"}]}`))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := c.Load(context.Background(), url); err != nil {
			t.Fatalf("Load #%d: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("HTTP calls = %d, want 1 (rest from cache)", got)
	}
}

func TestLoad_ResetCache_ForcesRefetch(t *testing.T) {
	var calls atomic.Int32
	url, client := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"schema_version":1,"plugins":[]}`))
	}))
	c := NewClient(client, logging.Nop{}, time.Minute)
	_, _ = c.Load(context.Background(), url)
	c.ResetCache()
	_, _ = c.Load(context.Background(), url)
	if got := calls.Load(); got != 2 {
		t.Errorf("HTTP calls after ResetCache = %d, want 2", got)
	}
}

func TestLoad_HostNotInWhitelist_ErrConfigInvalid(t *testing.T) {
	// Construct a URL pointing at evil.example.com which is never whitelisted.
	c := NewClient(http.DefaultClient, logging.Nop{}, time.Minute)
	_, err := c.Load(context.Background(), "https://evil.example.com/repo.json")
	if !errors.Is(err, sdk.ErrConfigInvalid) {
		t.Errorf("err = %v, want wrap of ErrConfigInvalid", err)
	}
	if !strings.Contains(err.Error(), "whitelist") {
		t.Errorf("err message = %q, want it to mention whitelist", err.Error())
	}
}

// ensure imports are used even if the test cases above are stripped during edits
var _ = fmt.Sprintf
