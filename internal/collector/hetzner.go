package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// HetznerCollector collects server and firewall data from Hetzner Cloud.
type HetznerCollector struct {
	cfg    config.HetznerCollectorConfig
	appCfg *config.Config
	log    *slog.Logger
}

func NewHetznerCollector(cfg config.HetznerCollectorConfig, appCfg *config.Config, log *slog.Logger) *HetznerCollector {
	return &HetznerCollector{cfg: cfg, appCfg: appCfg, log: log}
}

func (h *HetznerCollector) Name() string      { return "hetzner" }
func (h *HetznerCollector) SourceType() string { return "native" }

func (h *HetznerCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	token, err := h.appCfg.ResolveSecret("hetzner_api_token")
	if err != nil {
		return nil, fmt.Errorf("resolve hetzner token: %w", err)
	}

	client := hcloud.NewClient(hcloud.WithToken(token))
	result := &model.CollectionResult{Source: "hetzner"}

	// Collect servers
	servers, err := client.Server.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}

	// Collect firewalls
	firewalls, err := client.Firewall.All(ctx)
	if err != nil {
		h.log.Warn("failed to fetch firewalls", "error", err)
	}

	// Build server-to-firewall map
	serverFW := make(map[int64]*hcloud.Firewall)
	for _, fw := range firewalls {
		for _, res := range fw.AppliedTo {
			if res.Type == hcloud.FirewallResourceTypeServer && res.Server != nil {
				serverFW[res.Server.ID] = fw
			}
		}
	}

	for _, srv := range servers {
		zone := "unknown"
		if srv.Labels != nil {
			if z, ok := srv.Labels["zone"]; ok {
				zone = z
			}
		}

		var monthlyCost float64
		if srv.ServerType.Pricings != nil && len(srv.ServerType.Pricings) > 0 {
			if p, err := strconv.ParseFloat(srv.ServerType.Pricings[0].Monthly.Gross, 64); err == nil {
				monthlyCost = p
			}
		}

		pubIPv4 := ""
		if srv.PublicNet.IPv4.IP != nil {
			pubIPv4 = srv.PublicNet.IPv4.IP.String()
		}

		// Build details with network exposure
		exposure := map[string]any{
			"has_public_ipv4": pubIPv4 != "",
			"has_public_ipv6": srv.PublicNet.IPv6.IP != nil,
			"server_type":     srv.ServerType.Name,
			"datacenter":      srv.Datacenter.Name,
			"labels":          srv.Labels,
		}

		// Analyze firewall for network exposure
		if fw, ok := serverFW[srv.ID]; ok {
			exposure["firewall_name"] = fw.Name
			publicPorts := []int{}
			for _, rule := range fw.Rules {
				if rule.Direction != hcloud.FirewallRuleDirectionIn {
					continue
				}
				isPublic := false
				for _, src := range rule.SourceIPs {
					s := src.String()
					if s == "0.0.0.0/0" || s == "::/0" {
						isPublic = true
						break
					}
				}
				if !isPublic {
					continue
				}
				if rule.Port != nil {
					// Simple port parsing
					var port int
					fmt.Sscanf(*rule.Port, "%d", &port)
					if port > 0 {
						publicPorts = append(publicPorts, port)
					}
				}
			}
			exposure["public_ports"] = publicPorts

			// Determine ingress type
			infraPorts := map[int]bool{41641: true} // Tailscale WireGuard
			hasAppPorts := false
			for _, p := range publicPorts {
				if !infraPorts[p] {
					hasAppPorts = true
					break
				}
			}
			if len(publicPorts) == 0 {
				exposure["ingress"] = "unknown"
			} else if hasAppPorts {
				exposure["ingress"] = "public"
			} else {
				exposure["ingress"] = "tailscale-only"
			}
		}

		detailsJSON, _ := json.Marshal(exposure)
		raw := json.RawMessage(detailsJSON)

		host := model.Host{
			Name:           srv.Name,
			Sources:        []string{"hetzner"},
			HostType:       "cloud",
			Status:         string(srv.Status),
			Zone:           zone,
			PublicIPv4:     pubIPv4,
			CPUCores:       srv.ServerType.Cores,
			MemoryMB:       int(srv.ServerType.Memory * 1024),
			DiskGB:         float64(srv.ServerType.Disk),
			MonthlyCostEUR: monthlyCost,
			Details:        &raw,
		}
		result.Hosts = append(result.Hosts, host)
	}

	// Store firewalls
	for _, fw := range firewalls {
		rulesJSON, _ := json.Marshal(fw.Rules)
		rulesRaw := json.RawMessage(rulesJSON)

		var appliedIDs []int64
		for _, res := range fw.AppliedTo {
			if res.Type == hcloud.FirewallResourceTypeServer && res.Server != nil {
				appliedIDs = append(appliedIDs, res.Server.ID)
			}
		}
		appliedJSON, _ := json.Marshal(appliedIDs)
		appliedRaw := json.RawMessage(appliedJSON)

		result.Firewalls = append(result.Firewalls, model.Firewall{
			Name:      fw.Name,
			Rules:     &rulesRaw,
			AppliedTo: &appliedRaw,
		})
	}

	h.log.Info("collected hetzner data", "servers", len(servers), "firewalls", len(firewalls))
	return result, nil
}
