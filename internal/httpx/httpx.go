// Package httpx centralises the HTTP policy from spec §9: HTTPS-only,
// host whitelist, capped redirect depth. All outbound requests in the
// plugin go through a client built here so the policy is enforced
// uniformly.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// AllowedHosts is the explicit allowlist from spec §9. Release-asset
// downloads from github.com 302 to objects.githubusercontent.com, so
// that host is included even though it never appears as a primary URL.
var AllowedHosts = map[string]struct{}{
	"api.github.com":                {},
	"raw.githubusercontent.com":     {},
	"github.com":                    {},
	"objects.githubusercontent.com": {},
}

// MaxRedirects caps redirect depth. Spec §9 says "bis Tiefe 5".
const MaxRedirects = 5

// NewClient returns an http.Client wired with the redirect cap and
// whitelist guard. A zero timeout means "no overall request timeout" —
// callers should rely on context deadlines for download-style requests
// where the wall-clock budget depends on asset size.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errors.New("httpx: too many redirects")
			}
			return AssertWhitelisted(req.URL.String())
		},
	}
}

// AssertWhitelisted rejects non-HTTPS URLs or hosts outside AllowedHosts.
// Returned errors are bare (no sentinel wrap); callers pick the right
// classification (typically ErrConfigInvalid for user-supplied URLs).
func AssertWhitelisted(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("non-HTTPS url not allowed: %s", redact(rawURL))
	}
	if _, ok := AllowedHosts[u.Host]; !ok {
		return fmt.Errorf("host not in whitelist: %s", u.Host)
	}
	return nil
}

// RedactURL strips userinfo, query, and fragment so a logged URL never
// carries a token. Spec §10: "Niemals repo_index_url mit eingebettetem
// Token loggen (defensiv abstreifen)."
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<unparseable>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// redact is the internal alias used by package-local error formatters.
func redact(rawURL string) string { return RedactURL(rawURL) }
