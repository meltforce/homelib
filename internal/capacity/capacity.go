package capacity

import (
	"encoding/json"
	"sort"

	"github.com/meltforce/homelib/internal/model"
)

// ComputeCapacity builds a capacity report from the full host list.
// It partitions hosts into nodes (hypervisors), guests (vm/lxc), and standalone,
// then aggregates allocations per node and per zone.
func ComputeCapacity(hosts []model.Host) *model.CapacityReport {
	var nodes []model.Host
	guestsByNode := make(map[string][]model.Host)
	var standaloneHosts []model.Host

	// Partition hosts
	for _, h := range hosts {
		switch h.HostType {
		case "node":
			nodes = append(nodes, h)
		case "vm", "lxc":
			parent := parentNode(h)
			if parent != "" {
				guestsByNode[parent] = append(guestsByNode[parent], h)
			}
		case "cloud", "device":
			standaloneHosts = append(standaloneHosts, h)
		}
	}

	report := &model.CapacityReport{
		StandaloneHosts: standaloneHosts,
	}

	zoneMap := make(map[string]*model.ZoneCapacity)

	for _, node := range nodes {
		guests := guestsByNode[node.Name]

		nc := model.NodeCapacity{
			Name:          node.Name,
			Zone:          node.Zone,
			Status:        node.Status,
			TotalCPU:      node.CPUCores,
			TotalMemoryMB: node.MemoryMB,
			Guests:        guests,
			GuestCount:    len(guests),
		}

		for _, g := range guests {
			nc.AllocCPU += g.CPUCores
			nc.AllocMemoryMB += g.MemoryMB
			nc.AllocDiskGB += g.DiskGB
		}

		nc.FreeCPU = nc.TotalCPU - nc.AllocCPU
		nc.FreeMemoryMB = nc.TotalMemoryMB - nc.AllocMemoryMB

		report.Nodes = append(report.Nodes, nc)

		// Aggregate into zone
		zc, ok := zoneMap[node.Zone]
		if !ok {
			zc = &model.ZoneCapacity{Zone: node.Zone}
			zoneMap[node.Zone] = zc
		}
		zc.TotalCPU += nc.TotalCPU
		zc.TotalMemoryMB += nc.TotalMemoryMB
		zc.AllocCPU += nc.AllocCPU
		zc.AllocMemoryMB += nc.AllocMemoryMB
		zc.FreeCPU += nc.FreeCPU
		zc.FreeMemoryMB += nc.FreeMemoryMB
		zc.NodeCount++
		zc.GuestCount += nc.GuestCount

		// Grand totals
		report.TotalCPU += nc.TotalCPU
		report.TotalMemoryMB += nc.TotalMemoryMB
		report.AllocCPU += nc.AllocCPU
		report.AllocMemoryMB += nc.AllocMemoryMB
		report.TotalGuests += nc.GuestCount
	}

	report.FreeCPU = report.TotalCPU - report.AllocCPU
	report.FreeMemoryMB = report.TotalMemoryMB - report.AllocMemoryMB
	report.TotalNodes = len(nodes)

	// Collect zones sorted by name
	for _, zc := range zoneMap {
		report.Zones = append(report.Zones, *zc)
	}
	sort.Slice(report.Zones, func(i, j int) bool {
		return report.Zones[i].Zone < report.Zones[j].Zone
	})

	// Sort nodes by name
	sort.Slice(report.Nodes, func(i, j int) bool {
		return report.Nodes[i].Name < report.Nodes[j].Name
	})

	return report
}

// parentNode extracts the Proxmox parent node name from a host's Details JSON.
// Expected format: {"proxmox": {"node": "dude"}}
func parentNode(h model.Host) string {
	if h.Details == nil {
		return ""
	}
	var details struct {
		Proxmox struct {
			Node string `json:"node"`
		} `json:"proxmox"`
	}
	if err := json.Unmarshal(*h.Details, &details); err != nil {
		return ""
	}
	return details.Proxmox.Node
}
