package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"os/exec"
	"strings"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// ProxmoxCollector collects node and guest data via Tailscale SSH + pvesh.
type ProxmoxCollector struct {
	cfg config.ProxmoxCollectorConfig
	log *slog.Logger
}

func NewProxmoxCollector(cfg config.ProxmoxCollectorConfig, log *slog.Logger) *ProxmoxCollector {
	return &ProxmoxCollector{cfg: cfg, log: log}
}

func (p *ProxmoxCollector) Name() string      { return "proxmox" }
func (p *ProxmoxCollector) SourceType() string { return "native" }

func (p *ProxmoxCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	result := &model.CollectionResult{Source: "proxmox"}

	// Collect nodes
	nodes, err := p.collectNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect nodes: %w", err)
	}
	result.Hosts = append(result.Hosts, nodes...)

	// Collect guests
	guests, err := p.collectGuests(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect guests: %w", err)
	}
	result.Hosts = append(result.Hosts, guests...)

	p.log.Info("collected proxmox data", "nodes", len(nodes), "guests", len(guests))
	return result, nil
}

func (p *ProxmoxCollector) sshCommand(ctx context.Context, command string) (string, error) {
	target := p.cfg.Host
	if p.cfg.User != "" {
		target = p.cfg.User + "@" + p.cfg.Host
	}

	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "LogLevel=ERROR",
		target, command)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w (stderr: %s)", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (p *ProxmoxCollector) collectNodes(ctx context.Context) ([]model.Host, error) {
	output, err := p.sshCommand(ctx, "pvesh get /nodes --output-format json")
	if err != nil {
		return nil, err
	}

	var nodesData []struct {
		Node   string `json:"node"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &nodesData); err != nil {
		return nil, fmt.Errorf("parse nodes: %w", err)
	}

	var hosts []model.Host
	for _, n := range nodesData {
		host := model.Host{
			Name:     n.Node,
			Source:   "proxmox",
			HostType: "node",
			Status:   n.Status,
			Zone:     "homelab",
		}

		// Get detailed status
		statusOut, err := p.sshCommand(ctx, fmt.Sprintf("pvesh get /nodes/%s/status --output-format json", n.Node))
		if err == nil {
			var status struct {
				CPUInfo struct {
					Model string `json:"model"`
					CPUs  int    `json:"cpus"`
				} `json:"cpuinfo"`
				Memory struct {
					Total int64 `json:"total"`
				} `json:"memory"`
			}
			if err := json.Unmarshal([]byte(statusOut), &status); err == nil {
				host.CPUCores = status.CPUInfo.CPUs
				host.MemoryMB = int(status.Memory.Total / (1024 * 1024))

				details := map[string]any{"cpu_model": status.CPUInfo.Model}
				detailsJSON, _ := json.Marshal(details)
				raw := json.RawMessage(detailsJSON)
				host.Details = &raw
			}
		}
		hosts = append(hosts, host)
	}
	return hosts, nil
}

func (p *ProxmoxCollector) collectGuests(ctx context.Context) ([]model.Host, error) {
	output, err := p.sshCommand(ctx, "pvesh get /cluster/resources --type vm --output-format json")
	if err != nil {
		return nil, err
	}

	var resources []struct {
		VMID   int     `json:"vmid"`
		Name   string  `json:"name"`
		Node   string  `json:"node"`
		Type   string  `json:"type"`
		Status string  `json:"status"`
		MaxCPU int     `json:"maxcpu"`
		MaxMem int64   `json:"maxmem"`
		MaxDisk int64  `json:"maxdisk"`
		Tags   string  `json:"tags"`
	}
	if err := json.Unmarshal([]byte(output), &resources); err != nil {
		return nil, fmt.Errorf("parse guests: %w", err)
	}

	var hosts []model.Host
	for _, r := range resources {
		// Parse tags
		var tags []string
		var tsIP, localIP string
		zone := "homelab"

		if r.Tags != "" {
			for _, tag := range strings.Split(r.Tags, ";") {
				tag = strings.TrimSpace(tag)
				if isIPAddress(tag) {
					if strings.HasPrefix(tag, "100.") {
						tsIP = tag
					} else if strings.HasPrefix(tag, "192.168.") {
						localIP = tag
					}
				} else {
					tags = append(tags, tag)
					if strings.HasPrefix(tag, "zone:") {
						zone = strings.SplitN(tag, ":", 2)[1]
					}
				}
			}
		}

		// Try to get IPs from API (more reliable than tags)
		apiTsIP, apiLocalIP := p.getGuestIPs(ctx, r.Node, r.VMID, r.Type)
		if apiTsIP != "" {
			tsIP = apiTsIP
		}
		if apiLocalIP != "" {
			localIP = apiLocalIP
		}

		memMB := int(r.MaxMem / (1024 * 1024))
		diskGB := math.Round(float64(r.MaxDisk)/(1024*1024*1024)*10) / 10

		details := map[string]any{
			"vmid": r.VMID,
			"node": r.Node,
			"tags": tags,
		}
		detailsJSON, _ := json.Marshal(details)
		raw := json.RawMessage(detailsJSON)

		hostType := "vm"
		if r.Type == "lxc" {
			hostType = "lxc"
		}

		hosts = append(hosts, model.Host{
			Name:        r.Name,
			Source:      "proxmox",
			HostType:    hostType,
			Status:      r.Status,
			Zone:        zone,
			TailscaleIP: tsIP,
			LocalIP:     localIP,
			CPUCores:    r.MaxCPU,
			MemoryMB:    memMB,
			DiskGB:      diskGB,
			Details:     &raw,
		})
	}
	return hosts, nil
}

func (p *ProxmoxCollector) getGuestIPs(ctx context.Context, node string, vmid int, guestType string) (tsIP, localIP string) {
	var cmd string
	switch guestType {
	case "lxc":
		cmd = fmt.Sprintf("pvesh get /nodes/%s/lxc/%d/interfaces --output-format json", node, vmid)
	case "qemu":
		cmd = fmt.Sprintf("pvesh get /nodes/%s/qemu/%d/agent/network-get-interfaces --output-format json", node, vmid)
	default:
		return
	}

	output, err := p.sshCommand(ctx, cmd)
	if err != nil || output == "" {
		return
	}

	if guestType == "lxc" {
		var ifaces []struct {
			Name        string `json:"name"`
			IPAddresses []struct {
				IPAddress string `json:"ip-address"`
			} `json:"ip-addresses"`
		}
		if err := json.Unmarshal([]byte(output), &ifaces); err != nil {
			return
		}
		for _, iface := range ifaces {
			if iface.Name == "lo" {
				continue
			}
			for _, ip := range iface.IPAddresses {
				if isIPAddress(ip.IPAddress) {
					if strings.HasPrefix(ip.IPAddress, "100.") {
						tsIP = ip.IPAddress
					} else if strings.HasPrefix(ip.IPAddress, "192.168.") {
						localIP = ip.IPAddress
					}
				}
			}
		}
	} else {
		var resp struct {
			Result []struct {
				Name        string `json:"name"`
				IPAddresses []struct {
					IPAddress string `json:"ip-address"`
				} `json:"ip-addresses"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(output), &resp); err != nil {
			return
		}
		for _, iface := range resp.Result {
			if iface.Name == "lo" || iface.Name == "Loopback Pseudo-Interface 1" {
				continue
			}
			for _, ip := range iface.IPAddresses {
				if isIPAddress(ip.IPAddress) {
					if strings.HasPrefix(ip.IPAddress, "100.") {
						tsIP = ip.IPAddress
					} else if strings.HasPrefix(ip.IPAddress, "192.168.") {
						localIP = ip.IPAddress
					}
				}
			}
		}
	}
	return
}

func isIPAddress(s string) bool {
	return net.ParseIP(s) != nil && strings.Contains(s, ".")
}
