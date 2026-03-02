package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/meltforce/homelib/internal/capacity"
	"github.com/meltforce/homelib/internal/model"
)

func (t *Tools) handleListHosts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filter := model.HostFilter{
		Source:   stringArg(req, "source"),
		Zone:     stringArg(req, "zone"),
		Status:   stringArg(req, "status"),
		HostType: stringArg(req, "type"),
		Search:   stringArg(req, "search"),
	}

	hosts, err := t.store.GetHosts(filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get hosts: %v", err)), nil
	}

	return jsonResult(hosts)
}

func (t *Tools) handleGetHost(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := stringArg(req, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	host, err := t.store.GetHost(name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get host: %v", err)), nil
	}
	if host == nil {
		return mcp.NewToolResultError(fmt.Sprintf("host %q not found", name)), nil
	}

	services, _ := t.store.GetServices(name, "")

	return jsonResult(map[string]any{
		"host":     host,
		"services": services,
	})
}

func (t *Tools) handleListServices(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	host := stringArg(req, "host")
	stack := stringArg(req, "stack")

	services, err := t.store.GetServices(host, stack)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get services: %v", err)), nil
	}

	return jsonResult(services)
}

func (t *Tools) handleListNetworks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	networks, err := t.store.GetNetworks()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get networks: %v", err)), nil
	}

	return jsonResult(networks)
}

func (t *Tools) handleGetACL(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	acl, err := t.store.GetTailscaleACL()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get ACL: %v", err)), nil
	}
	if acl == nil {
		return mcp.NewToolResultText("No ACL data available. Tailscale Control Plane API may not be enabled."), nil
	}

	return jsonResult(acl)
}

func (t *Tools) handleGetDNS(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dns, err := t.store.GetTailscaleDNS()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get DNS: %v", err)), nil
	}

	return jsonResult(dns)
}

func (t *Tools) handleGetRoutes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	routes, err := t.store.GetTailscaleRoutes()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get routes: %v", err)), nil
	}

	return jsonResult(routes)
}

func (t *Tools) handleListFindings(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	source := stringArg(req, "source")
	severity := stringArg(req, "severity")

	findings, err := t.store.GetFindings(source, severity)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get findings: %v", err)), nil
	}

	return jsonResult(findings)
}

func (t *Tools) handleGetSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	summary, err := t.store.GetSummary()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get summary: %v", err)), nil
	}

	return jsonResult(summary)
}

func (t *Tools) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := stringArg(req, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	hosts, services, findings, err := t.store.SearchInventory(query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	return jsonResult(map[string]any{
		"hosts":    hosts,
		"services": services,
		"findings": findings,
	})
}

func (t *Tools) handleGetCollectionStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	progress := t.orch.CurrentProgress()
	run, _ := t.store.GetLatestRun()

	return jsonResult(map[string]any{
		"current_progress": progress,
		"latest_run":       run,
	})
}

func (t *Tools) handleTriggerCollection(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	go func() {
		t.orch.Run(context.Background())
	}()

	return mcp.NewToolResultText("Collection started. Use get_collection_status to monitor progress."), nil
}

// stringArg extracts a string argument from the request.
func stringArg(req mcp.CallToolRequest, name string) string {
	return req.GetString(name, "")
}

func (t *Tools) handleGetCapacity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hosts, err := t.store.GetHosts(model.HostFilter{})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get hosts: %v", err)), nil
	}

	report := capacity.ComputeCapacity(hosts)

	nodeFilter := stringArg(req, "node")
	zoneFilter := stringArg(req, "zone")

	if nodeFilter != "" {
		for _, n := range report.Nodes {
			if n.Name == nodeFilter {
				return jsonResult(n)
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("node %q not found", nodeFilter)), nil
	}

	if zoneFilter != "" {
		for _, z := range report.Zones {
			if z.Zone == zoneFilter {
				return jsonResult(z)
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("zone %q not found", zoneFilter)), nil
	}

	return jsonResult(report)
}

// jsonResult creates a text result with JSON content.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
