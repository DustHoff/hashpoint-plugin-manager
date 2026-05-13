package installer

import (
	"strings"
	"testing"

	"github.com/dusthoff/hashpoint-plugin-manager/internal/github"
)

func TestRenderPattern_AllPlaceholders(t *testing.T) {
	got := renderPattern("{name}_{version}_{os}_{arch}.zip", "myplugin", "1.2.3", "linux", "arm64")
	want := "myplugin_1.2.3_linux_arm64.zip"
	if got != want {
		t.Errorf("renderPattern = %q, want %q", got, want)
	}
}

func TestRenderPattern_CustomPattern(t *testing.T) {
	got := renderPattern("{name}-{version}-{os}-{arch}.zip", "p", "1.0.0", "darwin", "amd64")
	if got != "p-1.0.0-darwin-amd64.zip" {
		t.Errorf("renderPattern = %q", got)
	}
}

func TestRenderPattern_UnknownPlaceholder_PassesThrough(t *testing.T) {
	// {unknown} is not substituted — surfaces missing-asset errors clearly.
	got := renderPattern("{name}_{unknown}.zip", "p", "1.0.0", "linux", "amd64")
	if !strings.Contains(got, "{unknown}") {
		t.Errorf("renderPattern = %q, want unknown placeholder preserved", got)
	}
}

func TestFindAsset_FoundAndMissing(t *testing.T) {
	assets := []github.Asset{
		{Name: "p_1.0.0_linux_amd64.zip"},
		{Name: "p_1.0.0_windows_amd64.zip"},
	}
	if a, ok := findAsset(assets, "p_1.0.0_linux_amd64.zip"); !ok || a.Name != "p_1.0.0_linux_amd64.zip" {
		t.Errorf("findAsset linux: got=%+v ok=%t", a, ok)
	}
	if _, ok := findAsset(assets, "p_1.0.0_darwin_amd64.zip"); ok {
		t.Errorf("findAsset darwin: ok=true, want false")
	}
}

func TestFindChecksumAsset_ExactMatch(t *testing.T) {
	assets := []github.Asset{
		{Name: "p_1.0.0_linux_amd64.zip"},
		{Name: "p_1.0.0_linux_amd64.zip.sha256"},
		{Name: "other.sha256"},
	}
	got, err := findChecksumAsset(assets, "p_1.0.0_linux_amd64.zip")
	if err != nil {
		t.Fatalf("findChecksumAsset: %v", err)
	}
	if got.Name != "p_1.0.0_linux_amd64.zip.sha256" {
		t.Errorf("got %q, want exact sha256 match", got.Name)
	}
}

func TestFindChecksumAsset_SingleFallback(t *testing.T) {
	// Only one *.sha256 in the release, not an exact match.
	assets := []github.Asset{
		{Name: "p_1.0.0_linux_amd64.zip"},
		{Name: "checksums.sha256"},
	}
	got, err := findChecksumAsset(assets, "p_1.0.0_linux_amd64.zip")
	if err != nil {
		t.Fatalf("findChecksumAsset: %v", err)
	}
	if got.Name != "checksums.sha256" {
		t.Errorf("got %q, want fallback checksums.sha256", got.Name)
	}
}

func TestFindChecksumAsset_NoneIsHardFail(t *testing.T) {
	assets := []github.Asset{{Name: "p_1.0.0_linux_amd64.zip"}}
	_, err := findChecksumAsset(assets, "p_1.0.0_linux_amd64.zip")
	if err == nil {
		t.Errorf("findChecksumAsset: nil error, want hard fail (no .sha256)")
	}
}

func TestFindChecksumAsset_MultipleAmbiguousIsHardFail(t *testing.T) {
	// Multiple *.sha256 but none exact-matches → ambiguous, reject.
	assets := []github.Asset{
		{Name: "p_1.0.0_linux_amd64.zip"},
		{Name: "p_1.0.0_other.sha256"},
		{Name: "different.sha256"},
	}
	_, err := findChecksumAsset(assets, "p_1.0.0_linux_amd64.zip")
	if err == nil {
		t.Errorf("findChecksumAsset: nil error, want hard fail (ambiguous)")
	}
}
