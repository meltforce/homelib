package crossref

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/meltforce/homelib/internal/model"
)

// tagToZone maps Tailscale tags to zones.
var tagToZone = map[string]string{
	"tag:server":        "homelab",
	"tag:private-cloud": "private-cloud",
	"tag:public-cloud":  "public-cloud",
}

// CrossReference enriches and validates hosts across sources.
type CrossReference struct {
	log *slog.Logger
}

func New(log *slog.Logger) *CrossReference {
	return &CrossReference{log: log}
}

// Run performs cross-referencing across all hosts and returns findings.
func (cr *CrossReference) Run(hosts []model.Host) (updated []model.Host, findings []model.Finding) {
	// Index hosts by source and name
	bySource := make(map[string]map[string]*model.Host)
	for i := range hosts {
		h := &hosts[i]
		if _, ok := bySource[h.Source]; !ok {
			bySource[h.Source] = make(map[string]*model.Host)
		}
		bySource[h.Source][strings.ToLower(h.Name)] = h
	}

	tsHosts := bySource["tailscale"]
	hetznerHosts := bySource["hetzner"]
	proxmoxHosts := bySource["proxmox"]

	// Hetzner ↔ Tailscale: match by hostname, add Tailscale IP
	if tsHosts != nil && hetznerHosts != nil {
		for name, hHost := range hetznerHosts {
			if tsHost, ok := tsHosts[name]; ok {
				if hHost.TailscaleIP == "" && tsHost.TailscaleIP != "" {
					hHost.TailscaleIP = tsHost.TailscaleIP
				}

				// Zone validation
				tsZone := zoneFromDetails(tsHost)
				if tsZone != "" && hHost.Zone != "" && hHost.Zone != "unknown" && tsZone != hHost.Zone {
					findings = append(findings, model.Finding{
						Source:      "crossref",
						FindingType: "zone_mismatch",
						Severity:    "warning",
						HostName:    hHost.Name,
						Message:     "Hetzner zone '" + hHost.Zone + "' differs from Tailscale zone '" + tsZone + "'",
					})
				}
			}
		}
	}

	// Proxmox ↔ Tailscale: validate zones
	if tsHosts != nil && proxmoxHosts != nil {
		for name, pHost := range proxmoxHosts {
			if pHost.HostType == "node" {
				continue // Skip nodes, only validate guests
			}
			if tsHost, ok := tsHosts[name]; ok {
				tsZone := zoneFromDetails(tsHost)
				if tsZone != "" && pHost.Zone != "" && tsZone != pHost.Zone {
					findings = append(findings, model.Finding{
						Source:      "crossref",
						FindingType: "zone_mismatch",
						Severity:    "warning",
						HostName:    pHost.Name,
						Message:     "Proxmox zone '" + pHost.Zone + "' differs from Tailscale zone '" + tsZone + "'",
					})
				}
			}
		}
	}

	return hosts, findings
}

// zoneFromDetails extracts zone from a Tailscale host's tags in details.
func zoneFromDetails(h *model.Host) string {
	if h.Details == nil {
		return ""
	}
	var details struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(*h.Details, &details); err != nil {
		return ""
	}
	for _, tag := range details.Tags {
		if z, ok := tagToZone[tag]; ok {
			return z
		}
	}
	return ""
}
