package github

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// CandidateOpts shapes the filtering rules from spec §4.
type CandidateOpts struct {
	IncludePrereleases bool
	HostAPIMajor       int
}

// FilterCandidates returns the subset of rels that:
//   - have a semver-conformant tag
//   - are not Draft
//   - are not Prerelease (unless opts.IncludePrereleases)
//   - satisfy the Major-Rule: semver.Major == "vN" for N=HostAPIMajor
//
// The returned slice is sorted descending by semver, so element 0 is
// "latest". Tag names are normalized to the "vX.Y.Z[…]" form.
func FilterCandidates(rels []Release, opts CandidateOpts) []Release {
	want := fmt.Sprintf("v%d", opts.HostAPIMajor)
	out := make([]Release, 0, len(rels))
	for _, r := range rels {
		if r.Draft {
			continue
		}
		if r.Prerelease && !opts.IncludePrereleases {
			continue
		}
		tag := normalizeTag(r.TagName)
		if !semver.IsValid(tag) {
			continue
		}
		if semver.Major(tag) != want {
			continue
		}
		r.TagName = tag
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return semver.Compare(out[i].TagName, out[j].TagName) > 0
	})
	return out
}

// FindByVersion returns the release whose normalized tag equals want,
// or false. Inputs are normalized to the "v" prefix form before compare.
func FindByVersion(rels []Release, want string) (Release, bool) {
	norm := normalizeTag(want)
	for _, r := range rels {
		if normalizeTag(r.TagName) == norm {
			return r, true
		}
	}
	return Release{}, false
}

func normalizeTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if !strings.HasPrefix(tag, "v") {
		return "v" + tag
	}
	return tag
}
