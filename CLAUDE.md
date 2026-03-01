# homelib

Go service that collects homelab inventory from multiple sources (Proxmox, Tailscale, Hetzner, Komodo, UniFi, plugins) and provides a Web UI, JSON API, and MCP server.

## Build & Run

```bash
# Build
go build -o homelib .

# Run locally (dev mode)
./homelib --local --config config.yaml

# Run with tsnet (production)
./homelib --config /data/config.yaml
```

## Project Structure

- `main.go` — Entry point: tsnet, HTTP server, scheduler
- `internal/config/` — YAML config with env-var + op:// secret resolution
- `internal/model/` — Go structs for all data types
- `internal/collector/` — Collector interface + implementations (proxmox, tailscale, hetzner, komodo, unifi, plugin)
- `internal/crossref/` — Cross-reference logic + role enrichment
- `internal/store/` — SQLite (WAL mode) persistence
- `internal/scheduler/` — Cron-based scheduling
- `internal/server/` — HTTP handlers (Web UI + JSON API)
- `internal/mcp/` — MCP server (Streamable HTTP on /mcp)
- `internal/web/` — Embedded templates + static assets

## Key Design Decisions

- **No auth** — Tailscale ACLs handle access control
- **tsnet** — Native Tailscale integration, no separate proxy
- **SQLite WAL** — Single-file DB, concurrent reads
- **Graceful degradation** — Failed collectors don't block others
- **Plugin system** — External scripts (local/SSH) with JSON output
- **All configurable** — No hardcoded values, everything in config.yaml
