package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
	"tailscale.com/client/tailscale"
	"tailscale.com/types/views"
)

// TailscaleCollector collects device info via the tsnet Local API
// and optionally ACLs/DNS/Routes via the Control Plane API.
type TailscaleCollector struct {
	cfg       config.TailscaleCollectorConfig
	appCfg    *config.Config
	localAPI  *tailscale.LocalClient
	log       *slog.Logger
}

func NewTailscaleCollector(cfg config.TailscaleCollectorConfig, appCfg *config.Config, lc *tailscale.LocalClient, log *slog.Logger) *TailscaleCollector {
	return &TailscaleCollector{cfg: cfg, appCfg: appCfg, localAPI: lc, log: log}
}

func (t *TailscaleCollector) Name() string      { return "tailscale" }
func (t *TailscaleCollector) SourceType() string { return "native" }

func (t *TailscaleCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	result := &model.CollectionResult{Source: "tailscale"}

	// Local API: peer devices
	status, err := t.localAPI.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("local API status: %w", err)
	}

	// Helper to convert views.Slice to []string
	sliceToStrings := func(tags *views.Slice[string]) []string {
		if tags == nil {
			return nil
		}
		var out []string
		for i := range tags.Len() {
			out = append(out, tags.At(i))
		}
		return out
	}

	// Self
	if status.Self != nil {
		selfHost := t.peerToHost(status.Self.HostName, status.Self.TailscaleIPs, status.Self.OS, true, sliceToStrings(status.Self.Tags))
		result.Hosts = append(result.Hosts, selfHost)
	}

	// Peers
	for _, peer := range status.Peer {
		// Skip Mullvad exit nodes
		tags := sliceToStrings(peer.Tags)
		hasMullvad := false
		for _, tag := range tags {
			if tag == "tag:mullvad-exit-node" {
				hasMullvad = true
				break
			}
		}
		if hasMullvad {
			continue
		}

		if len(peer.TailscaleIPs) == 0 {
			continue
		}

		host := t.peerToHost(peer.HostName, peer.TailscaleIPs, peer.OS, peer.Online, tags)
		result.Hosts = append(result.Hosts, host)
	}

	t.log.Info("collected tailscale devices", "count", len(result.Hosts))

	// Control Plane API (optional)
	if t.cfg.ControlPlane.Enabled {
		if err := t.collectControlPlane(ctx, result); err != nil {
			t.log.Warn("control plane collection failed", "error", err)
			result.Findings = append(result.Findings, model.Finding{
				Source:      "tailscale",
				FindingType: "collection_error",
				Severity:    "warning",
				Message:     fmt.Sprintf("Control Plane API failed: %v", err),
			})
		}
	}

	return result, nil
}

func (t *TailscaleCollector) peerToHost(hostname string, ips []netip.Addr, osName string, online bool, tags []string) model.Host {
	status := "offline"
	if online {
		status = "online"
	}

	tsIP := ""
	if len(ips) > 0 {
		tsIP = ips[0].String()
	}

	// Build details
	details := map[string]any{
		"os":   osName,
		"tags": tags,
	}
	detailsJSON, _ := json.Marshal(details)
	raw := json.RawMessage(detailsJSON)

	// Determine zone from tags
	zone := ""
	for _, tag := range tags {
		switch tag {
		case "tag:server":
			zone = "homelab"
		case "tag:private-cloud":
			zone = "private-cloud"
		case "tag:public-cloud":
			zone = "public-cloud"
		}
	}

	return model.Host{
		Name:        hostname,
		Sources:     []string{"tailscale"},
		HostType:    "tailscale",
		Status:      status,
		Zone:        zone,
		TailscaleIP: tsIP,
		Details:     &raw,
	}
}

func (t *TailscaleCollector) collectControlPlane(ctx context.Context, result *model.CollectionResult) error {
	apiKey, err := t.appCfg.ResolveSecret("ts_api_key")
	if err != nil {
		return fmt.Errorf("resolve API key: %w", err)
	}

	// We use raw HTTP calls to api.tailscale.com since the Go SDK v2 client
	// requires more setup. This keeps it simple.
	client := &http.Client{}
	baseURL := "https://api.tailscale.com/api/v2"

	doGet := func(path string) (json.RawMessage, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, path)
		}
		var raw json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		return raw, nil
	}

	// Determine tailnet name from self status
	tailnet := "-" // Use "-" for the authenticated user's tailnet

	// ACL Policy
	aclData, err := doGet("/tailnet/" + tailnet + "/acl")
	if err != nil {
		t.log.Warn("failed to fetch ACL", "error", err)
	} else {
		result.ACL = &model.TailscaleACL{ACLPolicy: &aclData}
	}

	// DNS
	dnsData, err := doGet("/tailnet/" + tailnet + "/dns/nameservers")
	if err != nil {
		t.log.Warn("failed to fetch DNS nameservers", "error", err)
	} else {
		searchData, _ := doGet("/tailnet/" + tailnet + "/dns/searchpaths")
		prefsData, _ := doGet("/tailnet/" + tailnet + "/dns/preferences")

		dns := &model.TailscaleDNS{
			Nameservers: &dnsData,
			SearchPaths: nil,
		}
		if searchData != nil {
			dns.SearchPaths = &searchData
		}
		// Parse MagicDNS from preferences
		if prefsData != nil {
			var prefs struct {
				MagicDNSEnabled bool `json:"magicDNSEnabled"`
			}
			json.Unmarshal(prefsData, &prefs)
			dns.MagicDNSEnabled = prefs.MagicDNSEnabled
		}
		result.DNS = dns
	}

	// Devices (for routes)
	devicesData, err := doGet("/tailnet/" + tailnet + "/devices")
	if err != nil {
		t.log.Warn("failed to fetch devices", "error", err)
	} else {
		var devResp struct {
			Devices []struct {
				Name       string   `json:"name"`
				Hostname   string   `json:"hostname"`
				Addresses  []string `json:"addresses"`
			} `json:"devices"`
		}
		if err := json.Unmarshal(devicesData, &devResp); err == nil {
			for _, d := range devResp.Devices {
				// Get routes for each device
				hostname := d.Hostname
				if hostname == "" {
					parts := strings.SplitN(d.Name, ".", 2)
					hostname = parts[0]
				}
				routesData, err := doGet("/device/" + d.Name + "/routes")
				if err != nil {
					continue
				}
				var routeResp struct {
					AdvertisedRoutes []string `json:"advertisedRoutes"`
					EnabledRoutes    []string `json:"enabledRoutes"`
				}
				if err := json.Unmarshal(routesData, &routeResp); err != nil {
					continue
				}
				if len(routeResp.AdvertisedRoutes) > 0 || len(routeResp.EnabledRoutes) > 0 {
					advJSON, _ := json.Marshal(routeResp.AdvertisedRoutes)
					enJSON, _ := json.Marshal(routeResp.EnabledRoutes)
					advRaw := json.RawMessage(advJSON)
					enRaw := json.RawMessage(enJSON)
					result.Routes = append(result.Routes, model.TailscaleRoute{
						DeviceName: hostname,
						Advertised: &advRaw,
						Enabled:    &enRaw,
					})
				}
			}
		}
	}

	// API Keys
	keysData, err := doGet("/tailnet/" + tailnet + "/keys")
	if err != nil {
		t.log.Warn("failed to fetch keys", "error", err)
	} else {
		var keysResp struct {
			Keys []struct {
				ID           string `json:"id"`
				Description  string `json:"description"`
				Created      string `json:"created"`
				Expires      string `json:"expires"`
				Capabilities json.RawMessage `json:"capabilities"`
			} `json:"keys"`
		}
		if err := json.Unmarshal(keysData, &keysResp); err == nil {
			for _, k := range keysResp.Keys {
				tk := model.TailscaleKey{
					KeyID:       k.ID,
					Description: k.Description,
				}
				if k.Capabilities != nil {
					caps := json.RawMessage(k.Capabilities)
					tk.Capabilities = &caps
				}
				result.Keys = append(result.Keys, tk)
			}
		}
	}

	return nil
}
