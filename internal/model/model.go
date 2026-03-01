package model

import (
	"encoding/json"
	"strings"
	"time"
)

// CollectionRun tracks a single inventory collection execution.
type CollectionRun struct {
	ID         int64            `json:"id"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Status     string           `json:"status"` // running, completed, failed
	DurationMs int64            `json:"duration_ms,omitempty"`
	Summary    *json.RawMessage `json:"summary,omitempty"`
}

// CollectionSource tracks per-source status within a run.
type CollectionSource struct {
	ID           int64      `json:"id"`
	RunID        int64      `json:"run_id"`
	Source       string     `json:"source"`       // proxmox, tailscale, hetzner, komodo, unifi, plugin:<name>
	SourceType   string     `json:"source_type"`  // native, plugin
	Status       string     `json:"status"`       // running, completed, failed, skipped
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	ItemCount    int        `json:"item_count"`
}

// Host represents any server, VM, container, or device in the inventory.
type Host struct {
	ID             int64            `json:"id"`
	RunID          int64            `json:"run_id"`
	Name           string           `json:"name"`
	Sources        []string         `json:"sources"`
	HostType       string           `json:"host_type"` // node, vm, lxc, cloud, device, tailscale
	Status         string           `json:"status"`    // running, stopped, online, offline
	Zone           string           `json:"zone"`      // homelab, private-cloud, public-cloud
	TailscaleIP    string           `json:"tailscale_ip,omitempty"`
	LocalIP        string           `json:"local_ip,omitempty"`
	PublicIPv4     string           `json:"public_ipv4,omitempty"`
	CPUCores       int              `json:"cpu_cores,omitempty"`
	MemoryMB       int              `json:"memory_mb,omitempty"`
	DiskGB         float64          `json:"disk_gb,omitempty"`
	Application    string           `json:"application,omitempty"`
	Category       string           `json:"category,omitempty"`
	MonthlyCostEUR float64          `json:"monthly_cost_eur,omitempty"`
	Details        *json.RawMessage `json:"details,omitempty"`
	ServiceCount   int              `json:"service_count,omitempty"`
}

// SourcesString returns sources joined with ", " for display in templates.
func (h Host) SourcesString() string {
	return strings.Join(h.Sources, ", ")
}

// HasSource returns true if the host has the given source.
func (h Host) HasSource(source string) bool {
	for _, s := range h.Sources {
		if s == source {
			return true
		}
	}
	return false
}

// Service represents a Docker container/service.
type Service struct {
	ID            int64            `json:"id"`
	RunID         int64            `json:"run_id"`
	HostName      string           `json:"host_name"`
	Source        string           `json:"source"` // komodo
	ServiceName   string           `json:"service_name"`
	ContainerName string           `json:"container_name,omitempty"`
	Image         string           `json:"image,omitempty"`
	StackName     string           `json:"stack_name,omitempty"`
	Details       *json.RawMessage `json:"details,omitempty"`
}

// Network represents a network/VLAN.
type Network struct {
	ID          int64            `json:"id"`
	RunID       int64            `json:"run_id"`
	Name        string           `json:"name"`
	VLANID      int              `json:"vlan_id,omitempty"`
	Subnet      string           `json:"subnet,omitempty"`
	Gateway     string           `json:"gateway,omitempty"`
	DHCPEnabled bool             `json:"dhcp_enabled"`
	Details     *json.RawMessage `json:"details,omitempty"`
}

// Firewall represents a cloud firewall or security group.
type Firewall struct {
	ID        int64            `json:"id"`
	RunID     int64            `json:"run_id"`
	Name      string           `json:"name"`
	Rules     *json.RawMessage `json:"rules,omitempty"`
	AppliedTo *json.RawMessage `json:"applied_to,omitempty"`
}

// TailscaleACL stores the full ACL policy.
type TailscaleACL struct {
	ID        int64            `json:"id"`
	RunID     int64            `json:"run_id"`
	ACLPolicy *json.RawMessage `json:"acl_policy"`
}

// TailscaleDNS stores DNS configuration.
type TailscaleDNS struct {
	ID              int64            `json:"id"`
	RunID           int64            `json:"run_id"`
	Nameservers     *json.RawMessage `json:"nameservers"`
	SearchPaths     *json.RawMessage `json:"search_paths"`
	MagicDNSEnabled bool             `json:"magic_dns_enabled"`
	SplitDNS        *json.RawMessage `json:"split_dns"`
}

// TailscaleRoute stores subnet routes for a device.
type TailscaleRoute struct {
	ID         int64            `json:"id"`
	RunID      int64            `json:"run_id"`
	DeviceName string           `json:"device_name"`
	Advertised *json.RawMessage `json:"advertised"`
	Enabled    *json.RawMessage `json:"enabled"`
}

// TailscaleKey stores API key metadata.
type TailscaleKey struct {
	ID           int64            `json:"id"`
	RunID        int64            `json:"run_id"`
	KeyID        string           `json:"key_id"`
	Description  string           `json:"description,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	ExpiresAt    time.Time        `json:"expires_at"`
	Capabilities *json.RawMessage `json:"capabilities,omitempty"`
}

// PluginMetrics stores arbitrary metrics from a plugin.
type PluginMetrics struct {
	ID         int64            `json:"id"`
	RunID      int64            `json:"run_id"`
	PluginName string           `json:"plugin_name"`
	Metrics    *json.RawMessage `json:"metrics"`
}

// Finding represents a warning or insight from validation/plugins.
type Finding struct {
	ID          int64            `json:"id"`
	RunID       int64            `json:"run_id"`
	Source      string           `json:"source"`
	FindingType string           `json:"finding_type"` // zone_mismatch, monitoring_gap, efficiency, etc.
	Severity    string           `json:"severity"`     // info, warning, error
	HostName    string           `json:"host_name,omitempty"`
	Message     string           `json:"message"`
	Details     *json.RawMessage `json:"details,omitempty"`
}

// CollectionResult is returned by each collector after a run.
type CollectionResult struct {
	Source   string
	Hosts    []Host
	Services []Service
	Networks []Network
	Firewalls []Firewall

	// Tailscale-specific
	ACL    *TailscaleACL
	DNS    *TailscaleDNS
	Routes []TailscaleRoute
	Keys   []TailscaleKey

	// Plugin-specific
	PluginMetrics *PluginMetrics
	Findings      []Finding
}

// PluginOutput is the expected JSON schema from plugin scripts.
type PluginOutput struct {
	Plugin   string         `json:"plugin"`
	Version  string         `json:"version"`
	Hosts    []PluginHost   `json:"hosts,omitempty"`
	Metrics  map[string]any `json:"metrics,omitempty"`
	Findings []PluginFinding `json:"findings,omitempty"`
}

// PluginHost is a host entry from a plugin.
type PluginHost struct {
	Name     string         `json:"name"`
	HostType string         `json:"host_type"`
	Details  map[string]any `json:"details,omitempty"`
}

// PluginFinding is a finding entry from a plugin.
type PluginFinding struct {
	Severity string `json:"severity"`
	HostName string `json:"host_name,omitempty"`
	Message  string `json:"message"`
}

// HostFilter for querying hosts.
type HostFilter struct {
	Source   string
	Zone     string
	Status   string
	HostType string
	Search   string
}

// Summary provides high-level inventory statistics.
type Summary struct {
	TotalHosts     int            `json:"total_hosts"`
	OnlineHosts    int            `json:"online_hosts"`
	TotalServices  int            `json:"total_services"`
	TotalNetworks  int            `json:"total_networks"`
	TotalFindings  int            `json:"total_findings"`
	HostsBySource  map[string]int `json:"hosts_by_source"`
	HostsByZone    map[string]int `json:"hosts_by_zone"`
	HostsByType    map[string]int `json:"hosts_by_type"`
	MonthlyCostEUR float64       `json:"monthly_cost_eur"`
	LastCollection *CollectionRun `json:"last_collection,omitempty"`
}
