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
