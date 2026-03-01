package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// PluginCollector runs external scripts and parses their JSON output.
type PluginCollector struct {
	cfg config.PluginConfig
	log *slog.Logger
}

func NewPluginCollector(cfg config.PluginConfig, log *slog.Logger) *PluginCollector {
	return &PluginCollector{cfg: cfg, log: log}
}

func (p *PluginCollector) Name() string       { return "plugin:" + p.cfg.Name }
func (p *PluginCollector) SourceType() string  { return "plugin" }

func (p *PluginCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	timeout := p.cfg.Timeout
	if timeout == 0 {
		timeout = 30_000_000_000 // 30s
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	var cmd *exec.Cmd

	switch p.cfg.Type {
	case "ssh":
		// Execute via Tailscale SSH
		target := p.cfg.Host
		if p.cfg.User != "" {
			target = p.cfg.User + "@" + p.cfg.Host
		}
		cmd = exec.CommandContext(ctx, "ssh", "-o", "StrictHostKeyChecking=accept-new", target, p.cfg.Command)
	case "local":
		cmd = exec.CommandContext(ctx, "sh", "-c", p.cfg.Command)
	default:
		return nil, fmt.Errorf("unknown plugin type: %s", p.cfg.Type)
	}

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	p.log.Info("running plugin", "name", p.cfg.Name, "type", p.cfg.Type, "command", p.cfg.Command)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("plugin %s: %w (stderr: %s)", p.cfg.Name, err, stderr.String())
	}

	var output model.PluginOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, fmt.Errorf("plugin %s: parse output: %w", p.cfg.Name, err)
	}

	result := &model.CollectionResult{
		Source: "plugin:" + p.cfg.Name,
	}

	// Convert plugin hosts to model hosts
	for _, ph := range output.Hosts {
		detailsJSON, _ := json.Marshal(ph.Details)
		raw := json.RawMessage(detailsJSON)
		result.Hosts = append(result.Hosts, model.Host{
			Name:     ph.Name,
			Sources:  []string{"plugin:" + p.cfg.Name},
			HostType: ph.HostType,
			Status:   "unknown",
			Details:  &raw,
		})
	}

	// Store metrics
	if output.Metrics != nil {
		metricsJSON, _ := json.Marshal(output.Metrics)
		raw := json.RawMessage(metricsJSON)
		result.PluginMetrics = &model.PluginMetrics{
			PluginName: p.cfg.Name,
			Metrics:    &raw,
		}
	}

	// Convert findings
	for _, pf := range output.Findings {
		result.Findings = append(result.Findings, model.Finding{
			Source:      "plugin:" + p.cfg.Name,
			FindingType: "plugin",
			Severity:    pf.Severity,
			HostName:    pf.HostName,
			Message:     pf.Message,
		})
	}

	return result, nil
}
