package crossref

import (
	"encoding/json"
	"log/slog"

	"github.com/meltforce/homelib/internal/model"
)

// tagToZone maps Tailscale tags to zones.
var tagToZone = map[string]string{
	"tag:server":        "homelab",
	"tag:private-cloud": "private-cloud",
	"tag:public-cloud":  "public-cloud",
}

// CrossReference validates merged hosts and produces findings.
type CrossReference struct {
	log *slog.Logger
}

func New(log *slog.Logger) *CrossReference {
	return &CrossReference{log: log}
}

// Run validates merged hosts and returns findings.
// After host merging, each host has combined sources and details keyed by source.
func (cr *CrossReference) Run(hosts []model.Host) ([]model.Host, []model.Finding) {
	var findings []model.Finding

	for i := range hosts {
		h := &hosts[i]

		// Zone validation: compare host zone with tailscale tag zone
		if !h.HasSource("tailscale") {
			continue
		}
		if h.HostType == "node" {
			continue
		}

		tsZone := zoneFromMergedDetails(h)
		if tsZone == "" || h.Zone == "" || h.Zone == "unknown" {
			continue
		}
		if tsZone != h.Zone {
			sourceLabel := "Host"
			if h.HasSource("hetzner") {
				sourceLabel = "Hetzner"
			} else if h.HasSource("proxmox") {
				sourceLabel = "Proxmox"
			}
			findings = append(findings, model.Finding{
				Source:      "crossref",
				FindingType: "zone_mismatch",
				Severity:    "warning",
				HostName:    h.Name,
				Message:     sourceLabel + " zone '" + h.Zone + "' differs from Tailscale zone '" + tsZone + "'",
			})
		}
	}

	return hosts, findings
}

// zoneFromMergedDetails extracts zone from tailscale details within merged details.
// Merged details have the form: {"tailscale": {..., "tags": [...]}, "proxmox": {...}}
func zoneFromMergedDetails(h *model.Host) string {
	if h.Details == nil {
		return ""
	}

	// Try merged format: {"tailscale": {"tags": [...]}}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(*h.Details, &merged); err != nil {
		return ""
	}

	tsDetails, ok := merged["tailscale"]
	if !ok {
		return ""
	}

	var details struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(tsDetails, &details); err != nil {
		return ""
	}

	for _, tag := range details.Tags {
		if z, ok := tagToZone[tag]; ok {
			return z
		}
	}
	return ""
}
