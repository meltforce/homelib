# Aufgabe: Komodo-Collector → dockhand-Collector ersetzen

> Self-contained Prompt-Doc für eine separate Claude-Session im homelib-Repo.
> Kontext kommt aus der Komodo→Ansible/dockhand-Migration im homelab-Repo
> (siehe Plan-File `~/.claude/plans/ich-berlege-komodo-abzul-sen-magical-wren.md`,
> Phase 5).

## Kontext

Im homelab wurde Komodo (UI-zentriertes Docker-Management) durch zwei
Tools ersetzt:

- **Ansible** (`configuration/docker-stacks/`) ist neue Single-Source für
  Stack-Deployments — alle Catalog-Hosts werden via `./run.sh` gepflegt.
- **dockhand** (auf watcher, `https://dockhand.leo-royal.ts.net/`) ist
  Operations-UI, gleichzeitig **die neue API-Datenquelle** für homelib.
  Aktuell sind dort 17 Environments registriert (id 1, 3-17, eines hat
  eine Lücke wegen Test-Cleanup).

**Komodo wird komplett deinstalliert** (Phase 6 der Migration). homelib
darf danach keine Komodo-API mehr aufrufen — sonst hängen die Collections
am toten Endpoint.

Diese Aufgabe ist **die einzige Hürde** vor dem Komodo-Teardown: wenn der
dockhand-Collector live geht, kann Komodo abgeschaltet werden.

## Ziel

Ersetze `internal/collector/komodo.go` durch `internal/collector/dockhand.go`,
sodass homelib den Container-Service-Inventar weiterhin produziert — nur
aus dockhand statt Komodo.

`model.Service`-Felder bleiben unverändert: `{HostName, Source,
ServiceName, ContainerName, Image, StackName}`. `Source` wird `"dockhand"`
statt `"komodo"`.

## dockhand-API-Reference (reverse-engineered)

Offizielle Doku unter `https://dockhand.pro/manual/` deutet die API nur
an. Felder/Pfade hier kommen aus Probing einer konkreten Version (Mai
2026) — bei Updates ggf. nachjustieren.

**Base-URL:** `https://dockhand.leo-royal.ts.net/api`
**Auth:** `Authorization: Bearer <token>`. Token liegt in setec unter
`docker/shared/dockhand-api-token`. Im homelib-Pattern: `secrets:` map
ergänzen (`dockhand_api_token: "homelab/dockhand-api-token"` ODER
`docker/shared/dockhand-api-token` — User-Konvention klären).

### Endpoints, die wir brauchen

| Methode | Pfad | Zweck |
|---|---|---|
| `GET` | `/environments` | Liste aller Hosts (id, name, host, port, connectionType, ...) |
| `POST` | `/environments/{id}/test` | Liefert `info.{serverVersion, containers, images, name}` als Sanity-Check vor `Collect` |
| `GET` | `/environments/{id}` | Einzelner Host (selten gebraucht) |

### Container-Liste pro Environment — TODO klären

Initiales Probing hat ergeben, dass `GET /environments/{id}/containers`
**nicht existiert** (HTTP 404 fallthrough auf SvelteKit-Index-HTML). Die
UI zeigt aber Container — also gibt es einen anderen Pfad.

**Wie finden:** im Browser dockhand-UI öffnen, DevTools → Network, zu
einem Environment klicken, der relevante XHR-Call zeigt den echten Pfad.
Wahrscheinlich-Kandidaten: `/api/containers?environmentId=X`,
`/api/environments/X/services`, `/api/docker/containers/X`. Skill
`dockhand-api` (im homelab-Repo unter `.claude/skills/dockhand-api/`)
hat Tipps zum Probing.

Sobald gefunden: response-schema dokumentieren in einem Memory-File und
hier nachpflegen.

### Compose-Labels (zentral für Stack-Mapping)

dockhand zieht Stack-Zuordnung aus den Standard-Compose-Labels:
- `com.docker.compose.project` → entspricht `StackName`
- `com.docker.compose.service` → entspricht `ServiceName`
- `Names[0]` (oder `name`) → `ContainerName`
- `Image` (mit oder ohne digest) → `Image`

Container ohne Compose-Labels (z. B. ad-hoc gestartete Container) werden
in dockhand trotzdem angezeigt; der Collector sollte sie überspringen
oder in einer eigenen "ungrouped"-Kategorie ablegen — analog wie der
Komodo-Collector `Template`-Stacks überspringt.

### HostName-Mapping

Pro Container: das Environment-Objekt hat ein `name`-Feld, das mit dem
Tailnet-Hostnamen identisch ist (z. B. "dawarich", "freshrss",
"vimmary-lxc"). Das ist der `HostName` für `model.Service`.

## Mapping Komodo → dockhand

| Komodo-Verhalten | dockhand-Pendant |
|---|---|
| `POST /read {type:"ListServers"}` → `id`/`name`-Map | `GET /environments` |
| `POST /read {type:"ListFullStacks"}` → `deployed_services` | Container-Endpoint pro Env (TODO klären) |
| `Template: true` skippen | Container ohne `com.docker.compose.project` Label skippen |
| `Source: "komodo"` | `Source: "dockhand"` |
| Header `X-Api-Key` + `X-Api-Secret` | Header `Authorization: Bearer <token>` |
| `tls.InsecureSkipVerify: true` | NICHT mehr nötig — Tailscale-LE-Cert ist gültig (default `tls.Config{}` reicht) |

## Konkrete Code-Änderungen

### `internal/collector/dockhand.go` — NEU

Analog zu `internal/collector/komodo.go`. Struktur:

```go
type DockhandCollector struct {
    cfg    config.DockhandCollectorConfig
    appCfg *config.Config
    log    *slog.Logger
}

func NewDockhandCollector(cfg config.DockhandCollectorConfig, appCfg *config.Config, log *slog.Logger) *DockhandCollector { ... }
func (d *DockhandCollector) Name() string      { return "dockhand" }
func (d *DockhandCollector) SourceType() string { return "native" }
func (d *DockhandCollector) Collect(ctx context.Context) (*model.CollectionResult, error) { ... }
```

`Collect` ruft `GET /environments` ab, iteriert, holt pro Environment die
Container-Liste, extrahiert Compose-Labels, baut `model.Service{}`-Slice,
gibt `*model.CollectionResult{Source: "dockhand", Services: ...}` zurück.

### `internal/config/config.go`

```go
type DockhandCollectorConfig struct {
    Enabled bool   `yaml:"enabled"`
    BaseURL string `yaml:"base_url"`
}

type CollectorsConfig struct {
    // ... bestehend bis auf Komodo
    Dockhand DockhandCollectorConfig `yaml:"dockhand"`  // ← neu
    // Komodo entfernt
}
```

### `main.go` (Zeile 127)

```go
if cfg.Collectors.Dockhand.Enabled {
    orch.Register(collector.NewDockhandCollector(cfg.Collectors.Dockhand, cfg, log))
}
// Komodo-Block entfernen
```

### `config.example.yaml`

```yaml
secrets:
  # ... unverändert bis auf:
  dockhand_api_token: "docker/shared/dockhand-api-token"
  # Komodo-Secrets entfernen:
  # komodo_api_key: ...
  # komodo_api_secret: ...

collectors:
  # ... unverändert bis auf:
  dockhand:
    enabled: true
    base_url: https://dockhand.leo-royal.ts.net
  # Komodo-Block entfernen
```

### `internal/collector/komodo.go` — ENTFERNEN

`git rm` nach erfolgreichem Test des dockhand-Collectors.

### `Dockerfile` / Image-Build

Keine Änderung nötig — kein Komodo-spezifisches Dependency. Nach Code-Edit:

```bash
# CI-Pipeline triggert auto-build via push-tag, oder manuell:
docker build -t meltforce/homelib:edge .
docker push meltforce/homelib:edge
```

### Deploy via `configuration/homelib/` im homelab-Repo

Nach Image-Push:

```bash
cd ~/projects/homelab/configuration/homelib
./run.sh
```

Das deployt den neuen Container auf homelib-lxc. Die `config.yaml.j2`-
Template muss vorher angepasst werden (komodo-section raus, dockhand-
section rein). Die Setec-Lookup-Mechanik bleibt — nur die Secret-Namen
ändern sich.

### Setec-Migration

Im homelab-Setec einmalig (nicht im homelib-Repo):

```bash
# Optional: alte Komodo-Secrets entfernen NACH erfolgreichem Cutover
setec rm homelab/komodo-api-key
setec rm homelab/komodo-api-secret

# dockhand-Token existiert schon (von der homelab-Migration angelegt):
setec list | grep dockhand
# → docker/shared/dockhand-api-token
```

`docker/shared/dockhand-api-token` ist ein cross-stack shared Secret —
wenn die User-Konvention im homelib-Setup die `homelab/`-Domain
bevorzugt, kann der Token auch dorthin kopiert werden. Memory
`feedback_setec_one_domain_per_project` im homelab-Memory dokumentiert
die `docker/`-Domain als deploy-time-Cross-Stack-Pool.

## Verifikation

1. **Lokal**: `go test ./internal/collector/...` (falls Tests existieren)
   plus manuell `go run main.go --config config.test.yaml --once` mit
   einem Test-Config, das nur den dockhand-Collector aktiviert hat.
2. **Sanity-Test der API** vor Deploy:
   ```bash
   TOKEN=$(setec get docker/shared/dockhand-api-token)
   curl -sk -H "Authorization: Bearer $TOKEN" \
     "https://dockhand.leo-royal.ts.net/api/environments" | jq
   ```
   → 17 Environments-Liste sichtbar.
3. **Live nach Deploy**: homelib's MCP-Server abfragen
   (`mcp__homelib__list_services`) — Container-Liste muss stimmen
   (16+ Hosts × Container, mit Compose-Labels). Vergleich gegen den
   alten Komodo-Stand: `git log` der homelib-DB-Run-Snapshots oder
   manueller Vergleich mit `docker ps` auf den Hosts.
4. **Nicht-Catalog-Hosts** (cast2md, karakeep, tsidp, homelib-lxc,
   caddy-lab-01/02): die laufen Stacks außerhalb des docker-stacks
   Catalogs, dockhand sieht sie aber via `tailscale serve`-Endpoint.
   Container müssen trotzdem im Inventar erscheinen.

## Open Questions (vor Implementation klären)

1. **Container-Endpoint** in dockhand-API genau finden (DevTools-Probing,
   siehe oben). Erst dann ist der Collector implementierbar.
2. **Setec-Domain für dockhand-Token**: bleibt es bei
   `docker/shared/dockhand-api-token` oder Kopie in `homelab/` für
   Konsistenz mit anderen homelib-Secrets?
3. **Backwards-Compat**: soll der Komodo-Collector parallel bleiben (mit
   `enabled: false` als Default) für Rollback-Pfad? Oder hard delete?
   Empfehlung: hard delete — Phase 6 der homelab-Migration entfernt
   Komodo, danach gibt es keinen Rollback-Pfad zur Komodo-API.

## Referenzen

- **homelab-Plan**: `~/.claude/plans/ich-berlege-komodo-abzul-sen-magical-wren.md`
- **dockhand-API-Skill** (homelab-Repo): `.claude/skills/dockhand-api/SKILL.md`
- **dockhand-API-Memory** (Claude-User-Memory):
  `~/.claude/projects/-Users-linus-projects-homelab/memory/reference_dockhand_api.md`
- **Vorhandener Komodo-Collector**: `internal/collector/komodo.go`
- **Setec-Konvention**: `docker/<stack>/<key>` für Stack-Secrets,
  `docker/shared/<key>` für Cross-Stack — siehe Memory
  `feedback_setec_one_domain_per_project.md`
