package httpx

import (
	"strings"
	"testing"
)

func TestAssertWhitelisted_AcceptsHTTPSAllowedHosts(t *testing.T) {
	for host := range AllowedHosts {
		url := "https://" + host + "/some/path"
		if err := AssertWhitelisted(url); err != nil {
			t.Errorf("AssertWhitelisted(%q) = %v, want nil", url, err)
		}
	}
}

func TestAssertWhitelisted_RejectsHTTP(t *testing.T) {
	url := "http://api.github.com/repos/foo/bar"
	err := AssertWhitelisted(url)
	if err == nil {
		t.Fatalf("AssertWhitelisted(%q) = nil, want HTTP rejection", url)
	}
	if !strings.Contains(err.Error(), "non-HTTPS") {
		t.Errorf("error message = %q, want it to mention non-HTTPS", err.Error())
	}
}

func TestAssertWhitelisted_RejectsForeignHost(t *testing.T) {
	url := "https://evil.example.com/repo.json"
	err := AssertWhitelisted(url)
	if err == nil {
		t.Fatalf("AssertWhitelisted(%q) = nil, want host rejection", url)
	}
	if !strings.Contains(err.Error(), "whitelist") {
		t.Errorf("error message = %q, want it to mention whitelist", err.Error())
	}
}

func TestAssertWhitelisted_RejectsMalformed(t *testing.T) {
	if err := AssertWhitelisted("://oops"); err == nil {
		t.Errorf("malformed URL accepted")
	}
}

func TestRedactURL_StripsTokenComponents(t *testing.T) {
	cases := []struct{ in, want string }{
		// userinfo stripped
		{"https://user:pass@raw.githubusercontent.com/o/r/main/repo.json",
			"https://raw.githubusercontent.com/o/r/main/repo.json"},
		// query stripped
		{"https://api.github.com/repos/o/r/releases?access_token=abc&page=2",
			"https://api.github.com/repos/o/r/releases"},
		// fragment stripped
		{"https://raw.githubusercontent.com/o/r/main/repo.json#secret",
			"https://raw.githubusercontent.com/o/r/main/repo.json"},
		// nothing to strip
		{"https://api.github.com/repos/o/r/releases",
			"https://api.github.com/repos/o/r/releases"},
	}
	for _, c := range cases {
		if got := RedactURL(c.in); got != c.want {
			t.Errorf("RedactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactURL_Unparseable(t *testing.T) {
	if got := RedactURL("://oops"); got != "<unparseable>" {
		t.Errorf("RedactURL(unparseable) = %q, want <unparseable>", got)
	}
}
