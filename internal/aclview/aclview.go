package aclview

import (
	"encoding/json"
	"strings"
)

type policy struct {
	Grants []grant `json:"grants"`
}

type grant struct {
	Src []string       `json:"src"`
	Dst []string       `json:"dst"`
	IP  []string       `json:"ip"`
	App map[string]any `json:"app"`
}

// DataflowRow is a single parsed "who can talk to whom" entry.
type DataflowRow struct {
	Source string
	Dest   string
	Ports  string // "tcp:443, tcp:80" or "*"
	Type   string // "network" or app capability name
}

// ParseDataflows extracts grant entries from a Tailscale ACL policy JSON
// and returns a flat list of dataflow rows for display.
func ParseDataflows(aclJSON json.RawMessage) []DataflowRow {
	var p policy
	if err := json.Unmarshal(aclJSON, &p); err != nil {
		return nil
	}

	var rows []DataflowRow
	for _, g := range p.Grants {
		if isDefaultGrant(g) {
			continue
		}

		src := strings.Join(g.Src, ", ")
		dst := strings.Join(g.Dst, ", ")

		if len(g.IP) > 0 {
			ports := strings.Join(g.IP, ", ")
			rows = append(rows, DataflowRow{
				Source: src,
				Dest:   dst,
				Ports:  ports,
				Type:   "network",
			})
		}

		for capName := range g.App {
			rows = append(rows, DataflowRow{
				Source: src,
				Dest:   dst,
				Ports:  "-",
				Type:   capName,
			})
		}
	}

	return rows
}

// isDefaultGrant returns true for standard Tailscale default grants
// (owner/admin/member → self) that aren't homelab-specific.
func isDefaultGrant(g grant) bool {
	defaults := map[string]bool{
		"autogroup:owner":  true,
		"autogroup:admin":  true,
		"autogroup:member": true,
	}
	for _, s := range g.Src {
		if !defaults[s] {
			return false
		}
	}
	for _, d := range g.Dst {
		if d != "autogroup:self" {
			return false
		}
	}
	return true
}
