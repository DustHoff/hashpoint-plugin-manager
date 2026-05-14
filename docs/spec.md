# Spezifikation: Hashpoint Plugin Manager (Repository Manager)

## 1. Zweck

Der Plugin Manager ist ein Hashpoint-Plugin mit Capability `plugin_management`. Er fungiert als **Repository Manager**: er löst Plugin-Pakete über eine zentrale `repo.json` auf, fragt für jedes referenzierte Plugin die GitHub-Release-Liste ab und installiert Binaries inkl. `manifest.toml` nach `<PluginsDir>/<name>/`.

Subprozess-Lifecycle, DB-Cleanup und Self-Uninstall-Schutz übernimmt der Hashpoint-Host (siehe `CLAUDE.md` → SDK-Referenz). Diese Spezifikation deckt ausschließlich das ab, was im Handler liegt.

**Self-Update ist nicht vorgesehen** — der Manager kann sich nicht selbst stoppen, ein Update von sich selbst ist also Aufgabe von Hashpoint-Core, nicht dieses Plugins. Beim Versuch, den eigenen Plugin-Namen via `Update` oder `Install` zu adressieren, ist mit einem expliziten Fehler abzulehnen.

## 2. Kompatibilitäts-Regel (zentral)

> **`semver.Major(plugin_version) == sdk.HostAPIVersion`**

Diese Regel gilt für den Plugin Manager **und** für jedes Plugin, das er installiert. Sie ist die einzige Kompatibilitätsgarantie zwischen SDK und Plugin und ersetzt eine separate API-Versionsprüfung über das Manifest hinaus.

### 2.1 Konsequenzen

- Solange `sdk.HostAPIVersion == 1` gilt:
  - Der Manager wird ausschließlich als `1.x.y` released.
  - Aus `repo.json` werden nur Releases mit Tag-MAJOR `1` berücksichtigt — `2.x.y` und höher werden ignoriert (mit Debug-Log).
- Bei einem SDK-Bump auf `2`:
  - Der Manager wird neu als `2.0.0` released; eine `1.x.y`-Version bleibt für ältere Hashpoint-Installationen verfügbar.
  - Plugin-Autoren müssen Major-Bumps mitgehen — ein `1.x.y`-Plugin läuft niemals gegen ein SDK `2`.

### 2.2 Self-Check beim Start

Der Manager prüft in `Init` (oder `Configure`):
- Eigene Version (aus eigenem `manifest.toml` bzw. einer kompilierten Konstante) ist semver-konform.
- `semver.Major(eigene_version) == sdk.HostAPIVersion`.

Bei Mismatch: `fmt.Errorf("%w: manager major %s ≠ sdk.HostAPIVersion %d", sdk.ErrConfigInvalid, ..., sdk.HostAPIVersion)`. Der Host parkt den Manager in `state=failed` — bewusst hart, um inkompatible Installationen sichtbar zu machen.

## 3. Datenquellen

### 3.1 `repo.json` (Index)

- Liegt in **genau einem** öffentlichen GitHub-Repo (keine Multi-Source-Unterstützung), Rohabruf via `https://raw.githubusercontent.com/<owner>/<repo>/<ref>/repo.json`.
- URL ist Plugin-Konfiguration (`text`-Feld `repo_index_url`). **Optional**: Wenn leer, gilt der kompilierte Default `https://raw.githubusercontent.com/DustHoff/hashpoint-plugin-manager/main/repo.json` (siehe `config.DefaultRepoIndexURL`). Der Default-Index wird mit ausgeliefert (`repo.json` im Repo-Root) und ist die Quelle, wenn der User keine eigene URL setzt.
- Format: JSON-Objekt mit Schema-Version + Plugin-Liste.

**Schema (v1):**
```json
{
  "schema_version": 1,
  "plugins": [
    {
      "name": "oncall-jira",
      "repository": "owner/repo",
      "description": "Files Jira tickets for off-duty work",
      "capabilities": ["oncall_documentation"],
      "asset_pattern": "{name}_{version}_{os}_{arch}.zip"
    }
  ]
}
```

| Feld | Pflicht | Beschreibung |
|---|---|---|
| `schema_version` | ja | aktuell `1`; bei Mismatch ⇒ `ErrConfigInvalid` |
| `plugins[].name` | ja | endgültiger Verzeichnis- und Manifest-Name; muss matchen, was nach Install in `manifest.toml` steht |
| `plugins[].repository` | ja | `owner/repo` auf github.com |
| `plugins[].description` | nein | UI-Anzeige; Fallback auf GitHub-Repo-Description |
| `plugins[].capabilities` | nein | informativ; UI-Filterung |
| `plugins[].asset_pattern` | nein | Default: `{name}_{version}_{os}_{arch}.zip` |

> **Hinweis:** Ein `min_api_version`-Feld gibt es bewusst nicht — die Kompatibilität ergibt sich allein aus der Major-Version des Release-Tags (§2).

### 3.2 GitHub Releases pro Plugin

- Endpoint: `GET https://api.github.com/repos/{owner}/{repo}/releases` (List Releases).
- Authentifizierung optional via `password`-Feld `github_token` (PAT mit `public_repo`-Scope) — ohne Token gilt das anonyme Rate-Limit (60 req/h pro IP).
- Bei HTTP 403 mit Rate-Limit-Header oder HTTP 5xx ⇒ `ErrTransient`.

## 4. Versionierung

- **Schema:** Semantic Versioning 2.0.0 (`MAJOR.MINOR.PATCH[-prerelease][+build]`).
- Vergleich/Parsing über `golang.org/x/mod/semver` (akzeptiert das Tag-Präfix `v`).
- Nicht-semver-konforme Release-Tags werden ignoriert (Warn-Log).
- **Pre-Releases** (`-rc`, `-beta`, …) werden standardmäßig **ausgeblendet**. Aktivierbar über `boolean`-Feld `include_prereleases`.
- **Kandidatenmenge:** alle Releases eines Plugins, die
  1. semver-konform sind,
  2. nicht Draft sind,
  3. nicht Pre-Release (außer `include_prereleases=true`) und
  4. **`semver.Major == fmt.Sprintf("v%d", sdk.HostAPIVersion)`** erfüllen (§2).
- **Latest** = höchste Version aus der Kandidatenmenge.
- Lokal installierte Version wird aus `<PluginsDir>/<name>/manifest.toml` (`version`) gelesen. Update-Verfügbarkeit ⇔ `semver.Compare(remote, local) > 0`.

### 4.1 Version-Pinning

- Ein Plugin kann auf eine konkrete Version festgesetzt werden. Pins überschreiben die Latest-Auflösung.
- Konfiguration als JSON in `text`-Feld `pinned_versions`:
  ```json
  {"oncall-jira": "1.2.3", "oncall-otrs": "0.9.1"}
  ```
- Leer / nicht gesetzt ⇒ alle Plugins folgen Latest.
- Version-Strings ohne `v`-Präfix; intern wird normalisiert.
- **Pins müssen die Major-Regel (§2) erfüllen.** Ein Pin auf eine Version mit `Major ≠ sdk.HostAPIVersion` ⇒ `ErrConfigInvalid` in `Configure`.
- **Wirkung:**
  - `ListAvailable` liefert für gepinnte Plugins die gepinnte Version statt Latest.
  - `Install`/`Update` versuchen exakt die gepinnte Version aufzulösen.
  - Existiert die gepinnte Version nicht als Release ⇒ `ErrConfigInvalid` mit Hinweis auf Plugin-Name + gepinnte Version.
- JSON-Parse-Fehler in `pinned_versions` ⇒ `ErrConfigInvalid` in `Configure`.

## 5. Konfiguration (Manifest-Schema)

```toml
[config_schema.fields.repo_index_url]
label    = "Repo Index (raw URL zur repo.json — leer ⇒ Default DustHoff/hashpoint-plugin-manager@main)"
type     = "text"
required = false

[config_schema.fields.github_token]
label    = "GitHub PAT (optional, hebt Rate-Limit an)"
type     = "password"
required = false

[config_schema.fields.include_prereleases]
label    = "Pre-Releases anzeigen"
type     = "boolean"
required = false
default  = "false"

[config_schema.fields.pinned_versions]
label    = "Version-Pins (JSON: {\"name\":\"x.y.z\"})"
type     = "text"
required = false

[config_schema.fields.cache_ttl_seconds]
label    = "Cache-TTL für Index und Releases (Sekunden)"
type     = "text"
required = false
default  = "300"
```

## 6. Handler-Verhalten

### 6.1 `ListAvailable`

1. `repo.json` laden (Cache prüfen, sonst HTTP GET).
2. `schema_version` validieren.
3. Für jedes Plugin parallel (begrenzte Concurrency, z. B. 8) Releases laden.
4. Kandidatenmenge gemäß §4 bilden (insb. **Major-Filter**).
5. Ziel-Version ermitteln:
   - **gepinnt** ⇒ Pin-Version, falls in Kandidatenmenge; sonst Plugin auslassen + Warn-Log.
   - sonst ⇒ Latest aus Kandidatenmenge.
6. Plugins mit leerer Kandidatenmenge werden ausgelassen (Debug-Log: "no compatible release for sdk.HostAPIVersion=N").
7. Rückgabe: `[]sdk.AvailablePlugin` mit `Name`, `Version`, `Description`.

### 6.2 `Install(name)` / `Update(name)`

1. Wenn `name == Metadata.Name` (Self-Reference) ⇒ Fehler `"self-update not supported"`.
2. Plugin-Eintrag in `repo.json` suchen — fehlt er ⇒ `ErrConfigInvalid`.
3. Ziel-Version bestimmen (Pin oder Latest gemäß §4 / §6.1).
4. **Major-Regel validieren** (§2): falls verletzt (sollte durch §6.1 schon nicht passieren, aber defensiv) ⇒ `ErrConfigInvalid`.
5. Asset gemäß `asset_pattern` + Host-OS/-Arch im Release auflösen.
6. **Checksum-Asset auflösen:** zum Asset gehörendes `<asset>.sha256` (oder `*.sha256` im selben Release) muss vorhanden sein — **fehlt es ⇒ Hard-Fail** (non-sentinel error).
7. Asset und Checksum-Datei in **Temp-Verzeichnis** (`os.MkdirTemp`) herunterladen.
8. **Checksum prüfen** (SHA-256). Mismatch ⇒ non-sentinel error, Temp aufräumen.
9. ZIP entpacken; sicherstellen, dass Top-Level genau `<name>/` mit mindestens `<name>(.exe)` und `manifest.toml` enthält. Path-Traversal in Einträgen ablehnen (Zip-Slip-Schutz).
10. **Manifest-Cross-Check:** entpacktes `manifest.toml` parsen; `name` muss `<name>` matchen, und `semver.Major(version) == sdk.HostAPIVersion` (Defence in Depth gegen falsch gebaute Bundles).
11. **Verzeichnis platzieren:**
    - **Install:** `<PluginsDir>/<name>/` darf nicht existieren. Temp-Verzeichnis via `os.Rename` an die finale Stelle bewegen.
    - **Update:** Altes Verzeichnis nach `<PluginsDir>/<name>.old/` umbenennen → neues an Stelle bewegen → bei Erfolg `.old` löschen, bei Fehler rückrollen.
12. Keine DB-Aktionen.

### 6.3 `Uninstall(name)`

- Wenn `name == Metadata.Name` ⇒ Host lehnt vorher mit `ErrSelfUninstallRefused` ab; der Handler-Code muss diesen Fall nicht selbst behandeln, sollte ihn aber defensiv ebenfalls ablehnen.
- Verzeichnis `<PluginsDir>/<name>/` rekursiv entfernen.
- Existiert das Verzeichnis nicht, ist das **kein Fehler** (Idempotenz).
- Keine DB-Aktionen, keine Subprozess-Aktionen.

## 7. Fehlerklassifizierung

| Situation | Sentinel |
|---|---|
| Manager-Major ≠ `sdk.HostAPIVersion` (Self-Check) | `ErrConfigInvalid` |
| `repo.json` ungültig / Schema-Mismatch | `ErrConfigInvalid` |
| `pinned_versions` JSON-Parse-Fehler | `ErrConfigInvalid` |
| Gepinnte Version verletzt Major-Regel | `ErrConfigInvalid` |
| Gepinnte Version existiert nicht als kompatibles Release | `ErrConfigInvalid` |
| `repo_index_url` leer | — (Default greift, siehe §3.1) |
| `repo_index_url` gesetzt aber kein HTTPS | `ErrConfigInvalid` |
| HTTP 5xx, Netzwerk-Timeout, Rate-Limit | `ErrTransient` |
| HTTP 404 für Release / fehlendes Asset | non-sentinel error |
| Fehlende `.sha256`-Datei zum Asset | non-sentinel error |
| Checksum-Mismatch | non-sentinel error |
| Manifest-Cross-Check-Fehler (Name/Major) | non-sentinel error |
| ZIP-Entpacken schlägt fehl | non-sentinel error |
| Self-Update/-Install-Versuch | non-sentinel error (`"self-update not supported"`) |
| Stale `SecretHandle` (PAT) | `ErrUnknownSecretHandle` → User muss Token neu speichern |

Alle Errors via `fmt.Errorf("%w: <kontext>", sentinel)` wrappen.

## 8. Caching

- In-Memory pro Plugin-Instanz, kein Disk-Cache.
- TTL aus `cache_ttl_seconds` (Default 300 s).
- Getrennte Cache-Einträge für `repo.json` und für die Release-Liste pro Repo.
- Manueller Bust: `Configure`-Aufruf invalidiert alle Caches.

## 9. Security

- HTTPS für die user-konfigurierte `repo_index_url` — wird in `config.Parse` geprüft und ist die einzige stellenwert-relevante User-Eingabe (§3.1). Weitere URLs werden vom Katalog und der GitHub-API geliefert und sind in der Praxis ebenfalls HTTPS; das Plugin verifiziert sie nicht zusätzlich.
- **Keine** Host-Whitelist. Eine frühere Implementierung pinned die Subdomains von GitHub explizit (`objects.githubusercontent.com` u.a.); GitHub hat ihre Release-Asset-CDN-Hosts rotiert (`release-assets.githubusercontent.com`), was jeden Install mit einer irreführenden "transient failure" gebrochen hat. Der Katalog selbst (kompilierte Default-URL plus User-Konfiguration) ist die Autorität, welche Domains der Manager kontaktieren darf.
- Redirects folgen der Go-Default-Policy (max 10 Sprünge).
- ZIP-Entpacken: **Zip-Slip** verhindern (`filepath.Clean` + Präfix-Check gegen Temp-Wurzel); Symlink-Einträge ablehnen.
- Asset-Größenlimit (Default 100 MiB), konfigurierbar.
- SHA-256-Checksum für jedes Asset **verpflichtend** (siehe §6.2 Schritt 6).
- GitHub-Token nur on-demand via `host.RedeemSecret` lesen, in Memory halten, nicht loggen.

## 10. Logging

Strukturiert über `host.Log`:
- `info` — Install/Update/Uninstall-Start und -Erfolg mit `name`, `version`.
- `warn` — Plugin in `repo.json` ohne kompatibles Release, ignorierte Tags, gepinnte Version nicht auflösbar.
- `error` — Download-/Entpack-/Checksum-/Cross-Check-Fehler.
- `debug` — Cache-Hits/-Misses, HTTP-Statuscodes, ETag-Verwendung, übersprungene Major-Mismatches.

Niemals `repo_index_url` mit eingebettetem Token loggen (defensiv abstreifen).

## 11. Entscheidungen (festgeschrieben)

| # | Entscheidung |
|---|---|
| 1 | Self-Update ist **nicht** möglich (§1, §6.2 Schritt 1, §6.3). |
| 2 | Asset-Konvention: `{name}_{version}_{os}_{arch}.zip` (GoReleaser-kompatibel, §3.1). |
| 3 | Checksum-Prüfung ist **verpflichtend**; fehlt `.sha256` ⇒ Hard-Fail (§6.2 Schritt 6, §9). |
| 4 | **Genau eine** `repo.json`-Quelle, kein Multi-Source (§3.1). |
| 5 | Version-Pinning per `pinned_versions`-Config möglich (§4.1, §5). |
| 6 | **Kompatibilität via Major-Match:** `semver.Major(plugin_version) == sdk.HostAPIVersion` für Manager und alle Plugins (§2). |

## 12. Tests

Diese Sektion definiert das verpflichtende Testniveau. Ziel ist nicht Coverage-Maximum, sondern dass jede in §6/§7/§9 normierte Regel mindestens einen Test hat, der sie verteidigt.

### 12.1 Layout und Lauf

- Tests liegen neben dem Produktivcode (Go-Konvention: `foo.go` + `foo_test.go`).
- Lauf: `go test ./...` aus dem Repo-Root. Muss ohne Netzwerk grün laufen — alle HTTP-Aufrufe werden über `net/http/httptest` gemockt, alle Dateioperationen gegen `t.TempDir()`.
- Keine externen Fixtures außerhalb des Repos. ZIP-Bundles und Checksum-Dateien werden im Testcode programmatisch gebaut (`archive/zip`, `crypto/sha256`).

### 12.2 Pflicht-Testabdeckung pro Paket

| Paket | Mindestens abzudecken | Spec-Bezug |
|---|---|---|
| `internal/config` | `Parse`: Happy-Path; `repo_index_url` leer ⇒ Fallback auf `DefaultRepoIndexURL` (kein Fehler); gesetzte aber non-HTTPS / Parse-Fehler / `cache_ttl_seconds` ungültig / `include_prereleases` ungültig / `pinned_versions` JSON-Fehler / Pin verletzt Major-Rule ⇒ jeweils `ErrConfigInvalid`. Default-Werte für TTL und Prereleases; Default-URL hat HTTPS-Schema. | §2.2, §3.1, §4.1, §5, §7 |
| `internal/cache` | Hit innerhalb TTL, Miss nach Ablauf (via injizierter Fake-Clock), Miss nach `Reset`, `ttl=0` ⇒ kein Caching, Concurrency-smoke (paralleles Get/Set ohne race detector beschwerden). | §8 |
| `internal/httpx` | `RedactURL` entfernt Userinfo + Query + Fragment. (Eine frühere Host-Whitelist und HTTPS-Pflicht in dieser Schicht wurden mit dem CDN-Rotations-Vorfall entfernt — siehe §9.) | §9, §10 |
| `internal/catalog` | Über `httptest`: 200 Happy-Path mit Schema 1, Schema-Mismatch ⇒ `ErrConfigInvalid`, kaputtes JSON ⇒ `ErrConfigInvalid`, 404 ⇒ `ErrConfigInvalid`, 5xx ⇒ `ErrTransient`, 429 ⇒ `ErrTransient`, Body über Limit ⇒ `ErrConfigInvalid`, Cache-Hit serviert zweiten Aufruf ohne HTTP. | §3.1, §7, §8 |
| `internal/github` (resolve) | `FilterCandidates`: Drafts/Prereleases/Major-Mismatches gefiltert, Pre-Release-Toggle, Sortierung absteigend, leere Eingabe ⇒ leere Ausgabe; `FindByVersion` mit und ohne `v`-Präfix. | §4 |
| `internal/github` (client) | Über `httptest`: Bearer-Header gesetzt wenn Token-Provider liefert; nicht gesetzt wenn Provider nil; 403 + `X-RateLimit-Remaining: 0` ⇒ `ErrTransient`; 5xx/429 ⇒ `ErrTransient`; Pagination via `Link rel="next"`; Token-Provider-Fehler `ErrUnknownSecretHandle` propagiert unverändert. | §3.2, §7 |
| `internal/installer` (checksum) | `parseDigestFile`: Single-Hex-Form, sha256sum-Format mit und ohne `*`-Präfix, mehrzeilige Listing-Datei mit Pfad-Präfix (Basename-Match), Eintrag fehlt ⇒ Fehler. Voll-Round-Trip `verifyChecksum` mit selbst gebauter Archiv-Datei. | §6.2 Schritt 6/8, §9 |
| `internal/installer` (extract) | `unzipSafe`: Happy-Path mit verschachtelten Verzeichnissen; Eintrag mit absolutem Pfad abgewiesen; Eintrag mit `..` abgewiesen; Symlink-Eintrag abgewiesen. | §6.2 Schritt 9, §9 |
| `internal/installer` (manifest) | `crossCheckManifest`: Happy-Path; Manifest-Name-Mismatch ⇒ Fehler; Manifest-Major-Mismatch ⇒ Fehler; fehlendes Binary ⇒ Fehler; fehlendes `manifest.toml` ⇒ Fehler. | §6.2 Schritt 10 |
| `internal/installer` (installer) | `renderPattern` mit allen vier Platzhaltern und mit fehlenden; `findAsset`/`findChecksumAsset` mit exact-match, single-fallback, multiple-ambiguous (Fehler), none (Fehler). | §6.2 Schritt 5/6 |
| `internal/installer` (end-to-end) | Synthetisches ZIP-Bundle + zugehörige `.sha256` über `httptest` ausgeliefert; `Install` legt `<PluginsDir>/<name>/` an, `Update` swappt + räumt `.old` weg, fehlgeschlagene Stage rollt zurück, `Uninstall` ist idempotent (zweiter Aufruf nil). Top-Level-Verstoß ⇒ Fehler. | §6.2 komplett, §6.3 |

### 12.3 Was bewusst **nicht** getestet wird

- **Self-Update / Self-Uninstall:** das Plugin lehnt sie defensiv ab, der Host filtert sie eigentlich vorher. Ein Test des Manager-internen Guards reicht; den Host-Pfad nicht simulieren.
- **`sdk.Plugin` Lifecycle-Order:** Init→Metadata→Configure-Reihenfolge garantiert der Host. Eigener Test wäre ein Re-Test der `go-plugin`-Bibliothek.
- **RPC-Roundtrip:** `sdk.Serve` ist Bibliotheks-Verantwortung und SDK-getestet.
- **DPAPI-Decryption:** Secrets erreichen den Handler nur als Handle; die Klartext-Decryption ist Host-Sache.

### 12.4 Toolchain-Erwartungen

- **Pure-Go-Pflicht:** Build und Test laufen mit `CGO_ENABLED=0`. Keine cgo-Abhängigkeit darf in den direkten oder indirekten Modulgraph gelangen. Der Race-Detector (`-race`) ist damit aus — Concurrency wird stattdessen durch deterministische Tests (`cache.TTL` Fake-Clock, `ListAvailable`-Goroutinen-Smoke) und Code-Review abgesichert.
- CI-Befehle:
  ```
  CGO_ENABLED=0 go vet ./...
  CGO_ENABLED=0 go build ./...
  CGO_ENABLED=0 go test ./...
  ```
- Coverage informativ, keine Schwelle erzwungen — qualitatives Ziel: jede §7-Zeile hat einen Test, der genau diesen Sentinel produziert.