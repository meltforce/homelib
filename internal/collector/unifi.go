package collector

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// UniFiCollector collects network data from a UniFi Controller.
type UniFiCollector struct {
	cfg    config.UniFiCollectorConfig
	appCfg *config.Config
	log    *slog.Logger
}

func NewUniFiCollector(cfg config.UniFiCollectorConfig, appCfg *config.Config, log *slog.Logger) *UniFiCollector {
	return &UniFiCollector{cfg: cfg, appCfg: appCfg, log: log}
}

func (u *UniFiCollector) Name() string      { return "unifi" }
func (u *UniFiCollector) SourceType() string { return "native" }

func (u *UniFiCollector) Collect(ctx context.Context) (*model.CollectionResult, error) {
	username, err := u.appCfg.ResolveSecret("unifi_username")
	if err != nil {
		return nil, fmt.Errorf("resolve username: %w", err)
	}
	password, err := u.appCfg.ResolveSecret("unifi_password")
	if err != nil {
		return nil, fmt.Errorf("resolve password: %w", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Authenticate
	if err := u.authenticate(ctx, client, username, password); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	result := &model.CollectionResult{Source: "unifi"}
	site := u.cfg.Site
	if site == "" {
		site = "default"
	}

	// Collect networks
	networks, err := u.collectNetworks(ctx, client, site)
	if err != nil {
		u.log.Warn("failed to collect networks", "error", err)
	} else {
		result.Networks = networks
	}

	// Collect devices (as hosts)
	time.Sleep(500 * time.Millisecond) // Rate limit
	devices, err := u.collectDevices(ctx, client, site)
	if err != nil {
		u.log.Warn("failed to collect devices", "error", err)
	} else {
		result.Hosts = devices
	}

	u.log.Info("collected unifi data", "networks", len(result.Networks), "devices", len(result.Hosts))
	return result, nil
}

func (u *UniFiCollector) authenticate(ctx context.Context, client *http.Client, username, password string) error {
	body := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	req, err := http.NewRequestWithContext(ctx, "POST", u.cfg.BaseURL+"/api/auth/login",
		strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (u *UniFiCollector) apiGet(ctx context.Context, client *http.Client, site, endpoint string) (json.RawMessage, error) {
	url := fmt.Sprintf("%s/proxy/network/api/s/%s%s", u.cfg.BaseURL, site, endpoint)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (u *UniFiCollector) collectNetworks(ctx context.Context, client *http.Client, site string) ([]model.Network, error) {
	data, err := u.apiGet(ctx, client, site, "/rest/networkconf")
	if err != nil {
		return nil, err
	}

	var nets []struct {
		Name        string `json:"name"`
		VLANID      int    `json:"vlan"`
		Subnet      string `json:"ip_subnet"`
		Gateway     string `json:"gateway_ip"`
		DHCPEnabled bool   `json:"dhcpd_enabled"`
		Purpose     string `json:"purpose"`
	}
	if err := json.Unmarshal(data, &nets); err != nil {
		return nil, err
	}

	var networks []model.Network
	for _, n := range nets {
		details := map[string]any{"purpose": n.Purpose}
		detailsJSON, _ := json.Marshal(details)
		raw := json.RawMessage(detailsJSON)

		networks = append(networks, model.Network{
			Name:        n.Name,
			VLANID:      n.VLANID,
			Subnet:      n.Subnet,
			Gateway:     n.Gateway,
			DHCPEnabled: n.DHCPEnabled,
			Details:     &raw,
		})
	}
	return networks, nil
}

func (u *UniFiCollector) collectDevices(ctx context.Context, client *http.Client, site string) ([]model.Host, error) {
	data, err := u.apiGet(ctx, client, site, "/stat/device")
	if err != nil {
		return nil, err
	}

	var devs []struct {
		Name            string `json:"name"`
		Model           string `json:"model"`
		Type            string `json:"type"`
		IP              string `json:"ip"`
		MAC             string `json:"mac"`
		State           int    `json:"state"`
		NumPort         int    `json:"num_port"`
		ConnectedClients int   `json:"num_sta"`
	}
	if err := json.Unmarshal(data, &devs); err != nil {
		return nil, err
	}

	var hosts []model.Host
	for _, d := range devs {
		devType := "unknown"
		switch d.Type {
		case "usw":
			devType = "switch"
		case "uap":
			devType = "access_point"
		case "ugw", "udm":
			devType = "gateway"
		}

		status := "offline"
		if d.State == 1 {
			status = "online"
		}

		details := map[string]any{
			"model":             d.Model,
			"device_type":      devType,
			"mac":              d.MAC,
			"ports":            d.NumPort,
			"connected_clients": d.ConnectedClients,
		}
		detailsJSON, _ := json.Marshal(details)
		raw := json.RawMessage(detailsJSON)

		hosts = append(hosts, model.Host{
			Name:     d.Name,
			Source:   "unifi",
			HostType: "device",
			Status:   status,
			Zone:     "homelab",
			LocalIP:  d.IP,
			Details:  &raw,
		})
	}
	return hosts, nil
}
