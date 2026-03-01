package collector

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// KomodoCollector collects Docker stack/service data from Komodo.
type KomodoCollector struct {
	cfg    config.KomodoCollectorConfig
	appCfg *config.Config
	log    *slog.Logger
}

func NewKomodoCollector(cfg config.KomodoCollectorConfig, appCfg *config.Config, log *slog.Logger) *KomodoCollector {
	return &KomodoCollector{cfg: cfg, appCfg: appCfg, log: log}
}

func (k *KomodoCollector) Name() string      { return "komodo" }
func (k *KomodoCollector) SourceType() string { return "native" }

func (k *KomodoCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	apiKey, err := k.appCfg.ResolveSecret("komodo_api_key")
	if err != nil {
		return nil, fmt.Errorf("resolve API key: %w", err)
	}
	apiSecret, err := k.appCfg.ResolveSecret("komodo_api_secret")
	if err != nil {
		return nil, fmt.Errorf("resolve API secret: %w", err)
	}

	result := &model.CollectionResult{Source: "komodo"}

	// Skip TLS verification (internal service)
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Fetch servers for ID -> name mapping
	serverMap, err := k.fetchServers(ctx, client, apiKey, apiSecret)
	if err != nil {
		return nil, fmt.Errorf("fetch servers: %w", err)
	}

	// Fetch stacks
	stacks, err := k.fetchStacks(ctx, client, apiKey, apiSecret, serverMap)
	if err != nil {
		return nil, fmt.Errorf("fetch stacks: %w", err)
	}

	result.Services = stacks
	k.log.Info("collected komodo data", "services", len(stacks))
	return result, nil
}

func (k *KomodoCollector) komodoPost(ctx context.Context, client *http.Client, apiKey, apiSecret string, reqBody any) ([]byte, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", k.cfg.BaseURL+"/read", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("X-Api-Secret", apiSecret)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return io.ReadAll(resp.Body)
}

func (k *KomodoCollector) fetchServers(ctx context.Context, client *http.Client, apiKey, apiSecret string) (map[string]string, error) {
	data, err := k.komodoPost(ctx, client, apiKey, apiSecret, map[string]any{
		"type":   "ListServers",
		"params": map[string]any{},
	})
	if err != nil {
		return nil, err
	}

	var servers []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, err
	}

	m := make(map[string]string, len(servers))
	for _, s := range servers {
		m[s.ID] = s.Name
	}
	return m, nil
}

func (k *KomodoCollector) fetchStacks(ctx context.Context, client *http.Client, apiKey, apiSecret string, serverMap map[string]string) ([]model.Service, error) {
	data, err := k.komodoPost(ctx, client, apiKey, apiSecret, map[string]any{
		"type":   "ListFullStacks",
		"params": map[string]any{},
	})
	if err != nil {
		return nil, err
	}

	var stacks []struct {
		ID       struct{ OID string `json:"$oid"` } `json:"_id"`
		Name     string `json:"name"`
		Desc     string `json:"description"`
		Template bool   `json:"template"`
		Config   struct {
			ServerID string `json:"server_id"`
		} `json:"config"`
		Info struct {
			DeployedServices []struct {
				ServiceName   string `json:"service_name"`
				ContainerName string `json:"container_name"`
				Image         string `json:"image"`
			} `json:"deployed_services"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &stacks); err != nil {
		return nil, err
	}

	var services []model.Service
	for _, stack := range stacks {
		if stack.Template {
			continue
		}

		serverName := serverMap[stack.Config.ServerID]

		for _, svc := range stack.Info.DeployedServices {
			services = append(services, model.Service{
				HostName:      serverName,
				Source:        "komodo",
				ServiceName:   svc.ServiceName,
				ContainerName: svc.ContainerName,
				Image:         svc.Image,
				StackName:     stack.Name,
			})
		}
	}
	return services, nil
}
