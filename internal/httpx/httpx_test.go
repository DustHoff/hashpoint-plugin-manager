package httpx

import "testing"

func TestRedactURL_StripsTokenComponents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://user:pass@raw.githubusercontent.com/o/r/main/repo.json",
			"https://raw.githubusercontent.com/o/r/main/repo.json"},
		{"https://api.github.com/repos/o/r/releases?access_token=abc&page=2",
			"https://api.github.com/repos/o/r/releases"},
		{"https://raw.githubusercontent.com/o/r/main/repo.json#secret",
			"https://raw.githubusercontent.com/o/r/main/repo.json"},
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
