package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/semver"
)

// crossCheckManifest is the defence-in-depth gate from spec §6.2 step 10.
// It is invoked after extraction succeeds:
//   - <pluginRoot>/manifest.toml exists and parses
//   - manifest.name matches the catalog name (no surprise rebrand)
//   - manifest.version's major matches sdk.HostAPIVersion (so a wrongly
//     built bundle can't sneak past the host's manifest check)
//   - <pluginRoot>/<name>(.exe) binary exists
func crossCheckManifest(pluginRoot, name string, hostAPIMajor int) error {
	st, err := os.Stat(pluginRoot)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("bundle: expected plugin directory at %s", pluginRoot)
	}
	binName := name
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if _, err := os.Stat(filepath.Join(pluginRoot, binName)); err != nil {
		return fmt.Errorf("bundle: binary %s missing", binName)
	}

	manifestPath := filepath.Join(pluginRoot, "manifest.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("bundle: manifest.toml missing in %s", pluginRoot)
	}
	var m struct {
		Name       string `toml:"name"`
		Version    string `toml:"version"`
		APIVersion int    `toml:"api_version"`
	}
	if _, err := toml.DecodeFile(manifestPath, &m); err != nil {
		return fmt.Errorf("bundle: parse manifest.toml: %v", err)
	}
	if m.Name != name {
		return fmt.Errorf("bundle: manifest name %q does not match catalog name %q", m.Name, name)
	}
	v := m.Version
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return fmt.Errorf("bundle: manifest version %q not valid semver", m.Version)
	}
	want := fmt.Sprintf("v%d", hostAPIMajor)
	if semver.Major(v) != want {
		return fmt.Errorf("bundle: manifest major %s != sdk.HostAPIVersion %d", semver.Major(v), hostAPIMajor)
	}
	return nil
}
