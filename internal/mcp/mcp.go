package mcp

import (
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/meltforce/homelib/internal/collector"
	"github.com/meltforce/homelib/internal/store"
)

// NewHandler creates the MCP Streamable HTTP handler mounted at /mcp.
func NewHandler(st *store.Store, orch *collector.Orchestrator) (http.Handler, error) {
	s := server.NewMCPServer(
		"homelib",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	tools := &Tools{store: st, orch: orch}
	tools.Register(s)

	handler := server.NewStreamableHTTPServer(s)
	return handler, nil
}

// Tools holds MCP tool implementations.
type Tools struct {
	store *store.Store
	orch  *collector.Orchestrator
}

// Register adds all tools to the MCP server.
func (t *Tools) Register(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("list_hosts",
		mcp.WithDescription("List all hosts/servers/VMs in the inventory. Filter by source, zone, status, or type."),
		mcp.WithString("source", mcp.Description("Filter by source: proxmox, tailscale, hetzner, komodo, unifi")),
		mcp.WithString("zone", mcp.Description("Filter by zone: homelab, private-cloud, public-cloud")),
		mcp.WithString("status", mcp.Description("Filter by status: running, stopped, online, offline")),
		mcp.WithString("type", mcp.Description("Filter by host type: node, vm, lxc, cloud, device, tailscale")),
		mcp.WithString("search", mcp.Description("Free-text search across name, application, IP")),
	), t.handleListHosts)

	s.AddTool(mcp.NewTool("get_host",
		mcp.WithDescription("Get detailed information about a specific host, including services and network exposure."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Hostname to look up")),
	), t.handleGetHost)

	s.AddTool(mcp.NewTool("list_services",
		mcp.WithDescription("List Docker services/containers. Filter by host or stack name."),
		mcp.WithString("host", mcp.Description("Filter by host name")),
		mcp.WithString("stack", mcp.Description("Filter by stack name")),
	), t.handleListServices)

	s.AddTool(mcp.NewTool("list_networks",
		mcp.WithDescription("List UniFi networks/VLANs with subnet and DHCP info."),
	), t.handleListNetworks)

	s.AddTool(mcp.NewTool("get_acl_policy",
		mcp.WithDescription("Get the Tailscale ACL policy: access rules, groups, grants, tag assignments."),
	), t.handleGetACL)

	s.AddTool(mcp.NewTool("get_dns_config",
		mcp.WithDescription("Get Tailscale DNS configuration: MagicDNS, split DNS, nameservers."),
	), t.handleGetDNS)

	s.AddTool(mcp.NewTool("get_routes",
		mcp.WithDescription("Get Tailscale subnet routes and exit nodes per device."),
	), t.handleGetRoutes)

	s.AddTool(mcp.NewTool("list_findings",
		mcp.WithDescription("List infrastructure warnings and findings from validation and plugins."),
		mcp.WithString("source", mcp.Description("Filter by source")),
		mcp.WithString("severity", mcp.Description("Filter by severity: info, warning, error")),
	), t.handleListFindings)

	s.AddTool(mcp.NewTool("get_summary",
		mcp.WithDescription("High-level inventory summary: host counts by source/zone, costs, online/offline."),
	), t.handleGetSummary)

	s.AddTool(mcp.NewTool("search_inventory",
		mcp.WithDescription("Free-text search across all inventory data (hosts, services, findings)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
	), t.handleSearch)

	s.AddTool(mcp.NewTool("get_collection_status",
		mcp.WithDescription("Get the status of the latest or currently running collection."),
	), t.handleGetCollectionStatus)

	s.AddTool(mcp.NewTool("trigger_collection",
		mcp.WithDescription("Start a new inventory collection run."),
	), t.handleTriggerCollection)
}
