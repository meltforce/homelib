package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	Service    ServiceConfig              `yaml:"service"`
	Schedule   ScheduleConfig             `yaml:"schedule"`
	Secrets    map[string]string          `yaml:"secrets"`
	Collectors CollectorsConfig           `yaml:"collectors"`
	Plugins    []PluginConfig             `yaml:"plugins"`
	Roles      RolesConfig                `yaml:"roles"`

	// resolved secrets cache
	resolvedSecrets map[string]string
}

type ServiceConfig struct {
	Hostname string `yaml:"hostname"`
	StateDir string `yaml:"state_dir"`
	DataDir  string `yaml:"data_dir"`
	LogLevel string `yaml:"log_level"`
}

type ScheduleConfig struct {
	Cron          string `yaml:"cron"`
	RetentionDays int    `yaml:"retention_days"`
}

type CollectorsConfig struct {
	Proxmox   ProxmoxCollectorConfig   `yaml:"proxmox"`
	Tailscale TailscaleCollectorConfig `yaml:"tailscale"`
	Hetzner   HetznerCollectorConfig   `yaml:"hetzner"`
	Komodo    KomodoCollectorConfig    `yaml:"komodo"`
	UniFi     UniFiCollectorConfig     `yaml:"unifi"`
}

type ProxmoxCollectorConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	User    string `yaml:"user"`
}

type TailscaleCollectorConfig struct {
	Enabled      bool                       `yaml:"enabled"`
	ControlPlane TailscaleControlPlaneConfig `yaml:"control_plane"`
}

type TailscaleControlPlaneConfig struct {
	Enabled bool `yaml:"enabled"`
}

type HetznerCollectorConfig struct {
	Enabled bool `yaml:"enabled"`
}

type KomodoCollectorConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
}

type UniFiCollectorConfig struct {
	Enabled        bool   `yaml:"enabled"`
	BaseURL        string `yaml:"base_url"`
	Site           string `yaml:"site"`
	IncludeClients bool   `yaml:"include_clients"`
}

type PluginConfig struct {
	Name        string        `yaml:"name"`
	Enabled     bool          `yaml:"enabled"`
	Description string        `yaml:"description"`
	Type        string        `yaml:"type"` // ssh, local
	Host        string        `yaml:"host"`
	User        string        `yaml:"user"`
	Command     string        `yaml:"command"`
	Timeout     time.Duration `yaml:"timeout"`
	Schedule    string        `yaml:"schedule"` // "default" or cron expression
}

type RolesConfig struct {
	ProxmoxNodes          map[string]ProxmoxNodeRole   `yaml:"proxmox_nodes"`
	ApplicationCategories map[string]string            `yaml:"application_categories"`
	GuestOverrides        map[string]string            `yaml:"guest_overrides"`
	TailscaleDevices      map[string]TailscaleDeviceRole `yaml:"tailscale_devices"`
}

type ProxmoxNodeRole struct {
	InfrastructureRole     string `yaml:"infrastructure_role"`
	WorkloadSpecialization string `yaml:"workload_specialization,omitempty"`
	Description            string `yaml:"description,omitempty"`
}

type TailscaleDeviceRole struct {
	Role        string `yaml:"role"`
	Application string `yaml:"application,omitempty"`
	Description string `yaml:"description,omitempty"`
	ParentHost  string `yaml:"parent_host,omitempty"`
}

// Load reads config from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		resolvedSecrets: make(map[string]string),
	}

	// Apply defaults
	cfg.Service.Hostname = "homelib"
	cfg.Service.StateDir = "/data/tsnet"
	cfg.Service.DataDir = "/data"
	cfg.Service.LogLevel = "info"
	cfg.Schedule.Cron = "0 6,18 * * *"
	cfg.Schedule.RetentionDays = 30
	cfg.Collectors.Tailscale.Enabled = true

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// ResolveSecret resolves a secret by key using the priority chain:
// 1. Environment variable HOMELIB_<UPPER_KEY>
// 2. op:// reference (if OP_SERVICE_ACCOUNT_TOKEN is set)
// 3. Literal value from config
func (c *Config) ResolveSecret(key string) (string, error) {
	if v, ok := c.resolvedSecrets[key]; ok {
		return v, nil
	}

	// 1. Environment variable
	envKey := "HOMELIB_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
	if v := os.Getenv(envKey); v != "" {
		c.resolvedSecrets[key] = v
		return v, nil
	}

	raw, ok := c.Secrets[key]
	if !ok {
		return "", fmt.Errorf("secret %q not configured", key)
	}

	// 2. op:// reference
	if strings.HasPrefix(raw, "op://") {
		v, err := resolveOP(raw)
		if err != nil {
			return "", fmt.Errorf("resolve op:// for %q: %w", key, err)
		}
		c.resolvedSecrets[key] = v
		return v, nil
	}

	// 3. Literal value
	c.resolvedSecrets[key] = raw
	return raw, nil
}

// resolveOP calls the 1Password CLI to read a secret reference.
func resolveOP(ref string) (string, error) {
	cmd := exec.Command("op", "read", ref)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("op read %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}
