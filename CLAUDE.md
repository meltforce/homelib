# homelib

Go service that collects homelab inventory from multiple sources (Proxmox, Tailscale, Hetzner, Dockhand, UniFi, plugins) and provides a Web UI, JSON API, and MCP server.

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
- `internal/collector/` — Collector interface + implementations (proxmox, tailscale, hetzner, dockhand, unifi, plugin)
- `internal/crossref/` — Cross-reference logic + role enrichment
- `internal/store/` — SQLite (WAL mode) persistence
- `internal/scheduler/` — Cron-based scheduling
- `internal/server/` — HTTP handlers (Web UI + JSON API)
- `internal/mcp/` — MCP server (Streamable HTTP on /mcp)
- `internal/web/` — Embedded templates + static assets

## Key Design Decisions

- **No auth** — Tailscale ACLs handle access control
- **tsnet (inbound only)** — The tsnet listener provides the `:443` Web-UI / MCP endpoint with the homelib node identity. Outbound HTTP from collectors uses Go's default `http.Client` and routes through the container's bridge network, so it appears to the tailnet as the **host's** identity (the LXC's tags), not the tsnet listener's tags. Tailscale ACL grants for outbound calls must be written against the host tag accordingly.
- **SQLite WAL** — Single-file DB, concurrent reads
- **Graceful degradation** — Failed collectors don't block others
- **Plugin system** — External scripts (local/SSH) with JSON output
- **All configurable** — No hardcoded values, everything in config.yaml
