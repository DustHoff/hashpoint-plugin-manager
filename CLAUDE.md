# CLAUDE.md

## Projektkontext

Dieses Projekt implementiert ein **Hashpoint-Plugin** in Go, das die Capability **`plugin_management`** bereitstellt. Es agiert als **Plugin-Quelle (Catalog Source)** für den Hashpoint-Host: es listet installierbare Plugins, installiert/aktualisiert/deinstalliert sie als Datei-Bundles unter `<PluginsDir>/<name>/`.

Das Plugin läuft als eigenständiger Subprozess. Kommunikation mit dem Host erfolgt über `hashicorp/go-plugin` im **net/rpc-Modus** (yamux-multiplext). Ein Plugin-Crash crasht den Host nicht.

---

## ⚠️ Pflicht: SDK-Stand bei jeder Session prüfen

**Vor jeder inhaltlichen Arbeit am Plugin** ist die aktuelle SDK-Quelle abzurufen — Interfaces, Konstanten, Error-Sentinels, Manifest-Schema und Wire-Protokoll können sich ändern. **Niemals** auf in dieser Datei kopierte Signaturen verlassen; sie sind hier bewusst nicht enthalten.

### Quellen (in dieser Reihenfolge konsultieren)

1. **SDK-Quellcode** (Single Source of Truth):
   `https://github.com/DustHoff/hashpoint/blob/main/plugin/sdk/sdk.go`
2. **Plugin-System-Überblick:**
   `https://github.com/DustHoff/hashpoint/blob/main/docs/plugins/README.md`
3. **API-Referenz:**
   `https://github.com/DustHoff/hashpoint/blob/main/docs/plugins/api.md`
4. **Wire-Protokoll & Secret-Modell:**
   `https://github.com/DustHoff/hashpoint/blob/main/docs/plugins/protocol.md`

### Go-Modulpfad

`github.com/dusthoff/hashpoint` — SDK-Import demnach:

```go
import sdk "github.com/dusthoff/hashpoint/plugin/sdk"
```

> Das SDK lag bis 2026-05 unter `internal/plugin/sdk/` und wurde dann auf `plugin/sdk/` promotet. Beide Pfade dürfen niemals gleichzeitig referenziert werden — der alte Pfad ist gelöscht. In älteren Dokumenten taucht zudem `github.com/onesi/hashpoint` auf — **veraltet**, ignorieren. `go.mod` des Hashpoint-Repos ist verbindlich.

### Workflow vor Code-Änderungen

1. SDK-Quelldatei fetchen, `HostAPIVersion`-Konstante notieren.
2. Mit dem im Plugin verwendeten Wert (Manifest `api_version` + `Metadata.APIVersion`) abgleichen.
3. Bei Drift: API-Diff prüfen (Interfaces `Plugin`, `PluginManagementHandler`, `HostAPI`, sowie `Capability`-Konstanten, `FieldType`-Werte, Error-Sentinels).
4. Manifest und Code an aktuellen Stand angleichen, bevor neue Features hinzukommen.
5. Quelle bevorzugen: bei Widerspruch zwischen `sdk.go` und `docs/plugins/*.md` gilt **`sdk.go`**.

---

## Was sich nicht aus dem SDK ergibt (projektspezifisch)

### Aufgabe
- Capability: **`plugin_management`** (Verzeichnis siehe `Capability`-Konstanten im aktuellen SDK).
- Verhalten: Katalog bereitstellen, Bundles unter `<PluginsDir>/<name>/` schreiben/entfernen.

### Verantwortungsgrenzen (Host vs. Handler)

**Host übernimmt** — Handler **nicht** implementieren:
- Subprozess-Lifecycle (Stop vor `Update`, Start nach `Install`/`Update`).
- DB-Cleanup (`plugin_state`, `plugin_settings`) nach `Uninstall`.
- Self-Uninstall-Ablehnung (Handler bekommt den Call nie).
- Handshake, RPC-Wiring, Secret-Decryption.

**Handler übernimmt:**
- Reine Dateioperationen unter `<PluginsDir>/<name>/` (Binary + `manifest.toml`).
- Idempotenz bei wiederholten Aufrufen mit gleichem `name`.
- Validierungs- und Netzwerk-Logik für die Katalog-Quelle.

### Projektstruktur

Seit der SDK-Promotion auf `plugin/sdk/` lebt das Plugin in einem **eigenen Repo** mit dem Modulpfad `github.com/dusthoff/hashpoint-plugin-manager`. Layout:

```
hashpoint-plugin-manager/
├── cmd/plugin-manager/main.go        // sdk.Serve(plugin.New())
├── manifest.toml                     // Schema in §5 der spec.md
└── internal/
    ├── plugin/                        // sdk.Plugin + PluginManagementHandler-Glue
    ├── catalog/                       // repo.json-Quelle mit TTL-Cache
    ├── github/                        // Releases-API + Major-Filter
    ├── installer/                     // Download / SHA-256 / Zip-Slip / Atomic-Rename
    ├── config/                        // Configure-Parsing inkl. Pinned-Versions
    ├── httpx/                         // HTTPS-Only + Host-Whitelist + Redact
    ├── logging/                       // Logger-Interface über HostAPI.Log
    ├── cache/                         // Generische TTL-Cache
    └── version/                       // Manager-Name + -Version (Sync zu manifest.toml)
```

> SDK ist **nicht** mehr `internal/`. Das Plugin baut ohne `replace`-Direktive — `go mod tidy` zieht das SDK aus dem öffentlichen Modul.

### Build & Test
- Go-Version: `go 1.26` (siehe `go.mod`); muss zu `go.mod` des Hashpoint-Repos passen.
- **Pure-Go-Pflicht:** `CGO_ENABLED=0` für alle Build- und Test-Läufe. Keine cgo-Abhängigkeit darf hinzukommen — direkt nicht und transitiv nicht. Das bedeutet auch: **kein `go test -race`** (der Race-Detector benötigt cgo).
- Build: `CGO_ENABLED=0 go build ./...`
- Tests: `CGO_ENABLED=0 go test ./...` — Testabdeckung siehe `docs/spec.md` §12.
- Lint: `go vet ./...`, optional `golangci-lint run`.

> Auf dieser Maschine ist Go **nicht** im PATH. Volle Binary: `C:\Users\dho\sdk\go1.26.2\bin\go.exe`.

---

## Universelle Don'ts (unabhängig von SDK-Version)

- ❌ DB-Zugriffe oder Subprozess-Management im Handler — gehört zum Host.
- ❌ Self-Uninstall implementieren oder testen — Host lehnt ab.
- ❌ SDK-Konstanten (z. B. API-Version, Magic Cookie, Capability-Strings) im Plugin-Code als Literale duplizieren — immer über das SDK-Package referenzieren.
- ❌ Eigene Handshake-/RPC-Logik — `sdk.Serve` übernimmt das.
- ❌ Klartext-Secrets erwarten oder über RPC weitergeben — nur Handles.
- ❌ Secrets loggen oder auf Disk schreiben.
- ❌ Plugin-Name in Log-Calls voranstellen — Host attribuiert selbst.
- ❌ Annahmen aus dieser Datei über konkrete SDK-Signaturen treffen — **vorher SDK fetchen**.
- ❌ cgo-Abhängigkeiten einführen (direkt oder transitiv). Pure-Go-Build, `CGO_ENABLED=0` ist verbindlich. Bei Dependency-Auswahl prüfen, ob ein Paket `import "C"` oder `cgo`-Tags verwendet.