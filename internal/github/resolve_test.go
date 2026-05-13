package github

import (
	"testing"
)

func mkRelease(tag string, draft, prerelease bool) Release {
	return Release{TagName: tag, Draft: draft, Prerelease: prerelease}
}

func TestFilterCandidates_DropsDraftsAndPrereleases(t *testing.T) {
	rels := []Release{
		mkRelease("v1.2.0", false, false),
		mkRelease("v1.3.0", true, false),    // draft
		mkRelease("v1.4.0-rc.1", false, true), // prerelease
		mkRelease("v1.5.0", false, false),
	}
	got := FilterCandidates(rels, CandidateOpts{HostAPIMajor: 1})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (draft+prerelease dropped); got=%v", len(got), got)
	}
	if got[0].TagName != "v1.5.0" {
		t.Errorf("got[0].TagName = %q, want v1.5.0 (newest)", got[0].TagName)
	}
	if got[1].TagName != "v1.2.0" {
		t.Errorf("got[1].TagName = %q, want v1.2.0", got[1].TagName)
	}
}

func TestFilterCandidates_PrereleaseToggle(t *testing.T) {
	rels := []Release{
		mkRelease("v1.0.0", false, false),
		mkRelease("v1.1.0-beta.1", false, true),
	}
	got := FilterCandidates(rels, CandidateOpts{HostAPIMajor: 1, IncludePrereleases: true})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (prerelease included)", len(got))
	}
	if got[0].TagName != "v1.1.0-beta.1" {
		t.Errorf("got[0] = %q, want v1.1.0-beta.1 (sorts above 1.0.0 by semver order)", got[0].TagName)
	}
}

func TestFilterCandidates_MajorFilter(t *testing.T) {
	rels := []Release{
		mkRelease("v0.9.0", false, false),
		mkRelease("v1.0.0", false, false),
		mkRelease("v1.5.0", false, false),
		mkRelease("v2.0.0", false, false),
	}
	got := FilterCandidates(rels, CandidateOpts{HostAPIMajor: 1})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (only major=1)", len(got))
	}
	for _, r := range got {
		if r.TagName == "v0.9.0" || r.TagName == "v2.0.0" {
			t.Errorf("got %q in candidates, should be filtered by major", r.TagName)
		}
	}
}

func TestFilterCandidates_InvalidSemverDropped(t *testing.T) {
	rels := []Release{
		mkRelease("v1.0.0", false, false),
		mkRelease("garbage-tag", false, false),
		mkRelease("release-2024", false, false),
	}
	got := FilterCandidates(rels, CandidateOpts{HostAPIMajor: 1})
	if len(got) != 1 || got[0].TagName != "v1.0.0" {
		t.Errorf("got = %v, want [v1.0.0]", got)
	}
}

func TestFilterCandidates_NormalizesMissingVPrefix(t *testing.T) {
	rels := []Release{
		mkRelease("1.2.3", false, false), // no v prefix
	}
	got := FilterCandidates(rels, CandidateOpts{HostAPIMajor: 1})
	if len(got) != 1 || got[0].TagName != "v1.2.3" {
		t.Errorf("got = %v, want [v1.2.3] (normalized)", got)
	}
}

func TestFilterCandidates_EmptyInput(t *testing.T) {
	got := FilterCandidates(nil, CandidateOpts{HostAPIMajor: 1})
	if len(got) != 0 {
		t.Errorf("got = %v, want empty", got)
	}
}

func TestFindByVersion_WithAndWithoutVPrefix(t *testing.T) {
	rels := []Release{
		mkRelease("v1.0.0", false, false),
		mkRelease("v1.2.3", false, false),
	}
	for _, q := range []string{"v1.2.3", "1.2.3", "  1.2.3  "} {
		r, ok := FindByVersion(rels, q)
		if !ok {
			t.Errorf("FindByVersion(%q): not found", q)
			continue
		}
		if r.TagName != "v1.2.3" {
			t.Errorf("FindByVersion(%q): got %q, want v1.2.3", q, r.TagName)
		}
	}
}

func TestFindByVersion_NotFound(t *testing.T) {
	rels := []Release{mkRelease("v1.0.0", false, false)}
	if _, ok := FindByVersion(rels, "v9.9.9"); ok {
		t.Errorf("FindByVersion: returned ok=true for missing version")
	}
}
