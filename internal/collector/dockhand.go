package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// DockhandCollector collects Docker container/stack inventory from a Dockhand
// instance (https://dockhand.pro) via its REST API.
type DockhandCollector struct {
	cfg    config.DockhandCollectorConfig
	appCfg *config.Config
	log    *slog.Logger
}

func NewDockhandCollector(cfg config.DockhandCollectorConfig, appCfg *config.Config, log *slog.Logger) *DockhandCollector {
	return &DockhandCollector{cfg: cfg, appCfg: appCfg, log: log}
}

func (d *DockhandCollector) Name() string       { return "dockhand" }
func (d *DockhandCollector) SourceType() string { return "native" }

func (d *DockhandCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	token, err := d.appCfg.ResolveSecret("dockhand_api_token")
	if err != nil {
		return nil, fmt.Errorf("resolve API token: %w", err)
	}

	result := &model.CollectionResult{Source: "dockhand"}
	client := &http.Client{Timeout: 30 * time.Second}

	envs, err := d.fetchEnvironments(ctx, client, token)
	if err != nil {
		return nil, fmt.Errorf("fetch environments: %w", err)
	}

	for _, env := range envs {
		containers, err := d.fetchContainers(ctx, client, token, env.ID)
		if err != nil {
			d.log.Warn("dockhand env fetch failed",
				"env_id", env.ID, "env_name", env.Name, "error", err)
			continue
		}
		for _, c := range containers {
			result.Services = append(result.Services, c.toService(env.Name))
		}
	}

	d.log.Info("collected dockhand data",
		"environments", len(envs), "services", len(result.Services))
	return result, nil
}

type dockhandEnvironment struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type dockhandContainer struct {
	ID        string            `json:"id"`
	Names     []string          `json:"names"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	Created   json.RawMessage   `json:"created"`
	CreatedAt string            `json:"createdAt"`
	Labels    map[string]string `json:"labels"`
	Ports     []dockhandPort    `json:"ports"`

	Raw json.RawMessage `json:"-"`
}

// parseCreated handles three observed Dockhand variants:
// - unix seconds (int)
// - ISO-8601 string in `created`
// - ISO-8601 string in `createdAt`
func (c *dockhandContainer) parseCreated() *time.Time {
	if c.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, c.CreatedAt); err == nil {
			t = t.UTC()
			return &t
		}
	}
	if len(c.Created) > 0 {
		var ts int64
		if err := json.Unmarshal(c.Created, &ts); err == nil && ts > 0 {
			t := time.Unix(ts, 0).UTC()
			return &t
		}
		var s string
		if err := json.Unmarshal(c.Created, &s); err == nil && s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				t = t.UTC()
				return &t
			}
		}
	}
	return nil
}

type dockhandPort struct {
	IP          string `json:"ip"`
	PrivatePort int    `json:"privateport"`
	PublicPort  int    `json:"publicport"`
	Type        string `json:"type"`
}

// toService maps a Dockhand container into model.Service. Compose labels
// determine StackName/ServiceName; ad-hoc containers (no compose labels) get
// empty StackName and the container name as ServiceName.
func (c *dockhandContainer) toService(hostName string) model.Service {
	containerName := strings.TrimPrefix(c.Name, "/")
	if containerName == "" && len(c.Names) > 0 {
		containerName = strings.TrimPrefix(c.Names[0], "/")
	}

	stack := c.Labels["com.docker.compose.project"]
	service := c.Labels["com.docker.compose.service"]
	if service == "" {
		service = containerName
	}

	state := c.State
	if state == "" {
		state = c.Status
	}

	svc := model.Service{
		HostName:      hostName,
		Source:        "dockhand",
		ServiceName:   service,
		ContainerName: containerName,
		Image:         c.Image,
		StackName:     stack,
		Status:        state,
		Ports:         formatPorts(c.Ports),
	}
	if t := c.parseCreated(); t != nil {
		svc.CreatedAt = t
	}
	if len(c.Raw) > 0 {
		raw := json.RawMessage(c.Raw)
		svc.Details = &raw
	}
	return svc
}

// formatPorts renders a Docker-style port list as "8080->80/tcp, 443/tcp".
func formatPorts(ports []dockhandPort) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	seen := make(map[string]bool)
	for _, p := range ports {
		var s string
		if p.PublicPort != 0 {
			s = fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		} else {
			s = fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		parts = append(parts, s)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func (d *DockhandCollector) fetchEnvironments(ctx context.Context, client *http.Client, token string) ([]dockhandEnvironment, error) {
	body, err := d.get(ctx, client, token, "/api/environments")
	if err != nil {
		return nil, err
	}
	var envs []dockhandEnvironment
	if err := json.Unmarshal(body, &envs); err != nil {
		return nil, fmt.Errorf("decode environments: %w", err)
	}
	return envs, nil
}

func (d *DockhandCollector) fetchContainers(ctx context.Context, client *http.Client, token string, envID int) ([]dockhandContainer, error) {
	path := fmt.Sprintf("/api/containers?env=%d", envID)
	body, err := d.get(ctx, client, token, path)
	if err != nil {
		return nil, err
	}

	// Two-pass: first decode into typed struct for known fields, then keep the
	// raw element JSON in Details so future fields surface in the UI without
	// schema changes.
	var rawList []json.RawMessage
	if err := json.Unmarshal(body, &rawList); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}

	containers := make([]dockhandContainer, 0, len(rawList))
	for _, raw := range rawList {
		var c dockhandContainer
		if err := json.Unmarshal(raw, &c); err != nil {
			d.log.Warn("dockhand container decode failed", "env_id", envID, "error", err)
			continue
		}
		c.Raw = raw
		containers = append(containers, c)
	}
	return containers, nil
}

func (d *DockhandCollector) get(ctx context.Context, client *http.Client, token, path string) ([]byte, error) {
	url := strings.TrimRight(d.cfg.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d for %s: %s", resp.StatusCode, path, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
