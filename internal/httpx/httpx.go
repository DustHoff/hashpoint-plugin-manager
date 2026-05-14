// Package httpx is the thin HTTP layer used by catalog, github, and
// installer. It provides a shared http.Client constructor and a URL
// redactor for safe logging. A host whitelist used to live here but was
// dropped: the catalog (repo.json) is the authoritative source of
// truth for which URLs the manager touches, and GitHub freely rotates
// the CDN host that release-asset downloads redirect to — pinning a
// whitelist created an operational footgun (a CDN change broke every
// install with a misleading "transient failure").
package httpx

import (
	"net/http"
	"net/url"
	"time"
)

// NewClient returns an http.Client with the given timeout. A zero
// timeout means "no overall wall-clock limit" — callers should rely on
// per-request context deadlines for downloads. Go's default redirect
// policy (up to 10 hops) applies.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
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
