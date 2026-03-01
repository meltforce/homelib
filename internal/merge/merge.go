package merge

import (
	"encoding/json"
	"strings"

	"github.com/meltforce/homelib/internal/model"
)

// statusPriority defines preference for status values (lower = preferred).
var statusPriority = map[string]int{
	"running": 0,
	"online":  1,
	"stopped": 2,
	"offline": 3,
}

// typePriority defines preference for host types (lower = preferred).
var typePriority = map[string]int{
	"node":      0,
	"vm":        1,
	"lxc":       2,
	"cloud":     3,
	"device":    4,
	"tailscale": 5,
}

// MergeHosts deduplicates hosts from multiple collectors by matching on
// normalized hostname (primary) and tailscale_ip (secondary fallback).
// Returns a new slice with merged hosts.
func MergeHosts(hosts []model.Host) []model.Host {
	type group struct {
		hosts []*model.Host
	}

	groups := make(map[string]*group)       // normalized name → group
	byTSIP := make(map[string]*group)       // tailscale_ip → group
	var order []string                       // preserve first-seen order

	for i := range hosts {
		h := &hosts[i]
		key := strings.ToLower(h.Name)

		// Try matching by hostname first
		if g, ok := groups[key]; ok {
			g.hosts = append(g.hosts, h)
			continue
		}

		// Try matching by tailscale_ip
		if h.TailscaleIP != "" {
			if g, ok := byTSIP[h.TailscaleIP]; ok {
				g.hosts = append(g.hosts, h)
				// Also register this name → same group
				groups[key] = g
				continue
			}
		}

		// New group
		g := &group{hosts: []*model.Host{h}}
		groups[key] = g
		if h.TailscaleIP != "" {
			byTSIP[h.TailscaleIP] = g
		}
		order = append(order, key)
	}

	// Merge each group into a single host
	result := make([]model.Host, 0, len(order))
	for _, key := range order {
		g := groups[key]
		result = append(result, mergeGroup(g.hosts))
	}
	return result
}

func mergeGroup(hosts []*model.Host) model.Host {
	if len(hosts) == 1 {
		return *hosts[0]
	}

	merged := model.Host{}
	var sources []string
	detailsBySource := make(map[string]json.RawMessage)

	for _, h := range hosts {
		for _, s := range h.Sources {
			sources = appendUnique(sources, s)
		}

		// Name: prefer non-tailscale source name
		if merged.Name == "" || (isTailscaleOnly(merged.Sources) && !isTailscaleOnly(h.Sources)) {
			merged.Name = h.Name
		}

		// HostType: prefer by priority
		if merged.HostType == "" || betterType(h.HostType, merged.HostType) {
			merged.HostType = h.HostType
		}

		// Status: prefer most optimistic
		if merged.Status == "" || betterStatus(h.Status, merged.Status) {
			merged.Status = h.Status
		}

		// Zone: first non-empty, non-"unknown"
		if (merged.Zone == "" || merged.Zone == "unknown") && h.Zone != "" && h.Zone != "unknown" {
			merged.Zone = h.Zone
		} else if merged.Zone == "" && h.Zone != "" {
			merged.Zone = h.Zone
		}

		// IPs: first non-empty for each
		if merged.TailscaleIP == "" && h.TailscaleIP != "" {
			merged.TailscaleIP = h.TailscaleIP
		}
		if merged.LocalIP == "" && h.LocalIP != "" {
			merged.LocalIP = h.LocalIP
		}
		if merged.PublicIPv4 == "" && h.PublicIPv4 != "" {
			merged.PublicIPv4 = h.PublicIPv4
		}

		// Resources: first non-zero
		if merged.CPUCores == 0 && h.CPUCores != 0 {
			merged.CPUCores = h.CPUCores
		}
		if merged.MemoryMB == 0 && h.MemoryMB != 0 {
			merged.MemoryMB = h.MemoryMB
		}
		if merged.DiskGB == 0 && h.DiskGB != 0 {
			merged.DiskGB = h.DiskGB
		}

		// Application/Category: first non-empty
		if merged.Application == "" && h.Application != "" {
			merged.Application = h.Application
		}
		if merged.Category == "" && h.Category != "" {
			merged.Category = h.Category
		}

		// Cost: sum
		merged.MonthlyCostEUR += h.MonthlyCostEUR

		// Details: collect per source
		if h.Details != nil {
			for _, s := range h.Sources {
				detailsBySource[s] = *h.Details
			}
		}
	}

	merged.Sources = sources

	// Build merged details as {"proxmox": {...}, "tailscale": {...}}
	if len(detailsBySource) > 0 {
		combined, err := json.Marshal(detailsBySource)
		if err == nil {
			raw := json.RawMessage(combined)
			merged.Details = &raw
		}
	}

	return merged
}

func betterStatus(candidate, current string) bool {
	cp, cok := statusPriority[candidate]
	rp, rok := statusPriority[current]
	if !cok {
		return false
	}
	if !rok {
		return true
	}
	return cp < rp
}

func betterType(candidate, current string) bool {
	cp, cok := typePriority[candidate]
	rp, rok := typePriority[current]
	if !cok {
		return false
	}
	if !rok {
		return true
	}
	return cp < rp
}

func isTailscaleOnly(sources []string) bool {
	for _, s := range sources {
		if s != "tailscale" {
			return false
		}
	}
	return len(sources) > 0
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
