// Package version holds the Plugin Manager's own identity strings. Keep
// Manager in sync with manifest.toml's version field — the §2.2 self-check
// compares this constant to sdk.HostAPIVersion via semver.Major, and the
// host independently rejects any plugin whose manifest version disagrees
// with what Metadata() returns.
package version

// Manager is the Plugin Manager's semver version (no leading "v"; callers
// that compare via golang.org/x/mod/semver normalize). Bump in lockstep
// with the manifest.toml `version` field.
const Manager = "1.0.1"

// Name is this plugin's identifier under <PluginsDir>/<name>/, and the
// value Metadata() returns. Install/Update/Uninstall reject calls
// targeting this name (self-reference guard, spec §6.2 step 1 / §6.3).
const Name = "plugin-manager"
