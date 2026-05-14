# hashpoint-plugin-manager

A [Hashpoint](https://github.com/DustHoff/hashpoint) plugin that provides the `plugin_management` capability. It acts as a **catalog source**: it resolves available plugins via a GitHub-hosted `repo.json` index, fetches release artefacts from GitHub Releases with mandatory SHA-256 verification, and installs / updates / uninstalls them under the host's `PluginsDir`.

The full behaviour spec — repo.json schema, compatibility rules, error classification, security guarantees, test coverage matrix — lives in [`docs/spec.md`](docs/spec.md).

## Build

Pure-Go, no cgo:

```
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
```

The default `repo_index_url` points at this repository's `main` branch (`repo.json`); users can override it through the plugin's settings UI.

## Releases

Every push to `main` produces a GitHub release. Versioning is automated:

- Subject contains `feat`, `feature`, `add`, or `new` (whole word, case-insensitive) → **minor** bump.
- Anything else → **patch** bump.

Major bumps stay manual — they're reserved for SDK transitions (spec §2.1, e.g. `sdk.HostAPIVersion` going from 1 → 2). To cut a major, push a normal commit and then manually tag the result with `vN.0.0`.

Each release ships cross-compiled binaries for `linux/{amd64,arm64}`, `windows/{amd64,arm64}`, and `darwin/{amd64,arm64}`. Naming follows the spec convention this plugin enforces on plugins it installs: `plugin-manager_{version}_{os}_{arch}.zip` plus a matching `.sha256` file.
