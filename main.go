package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/meltforce/homelib/internal/collector"
	"github.com/meltforce/homelib/internal/config"
	mcpserver "github.com/meltforce/homelib/internal/mcp"
	"github.com/meltforce/homelib/internal/scheduler"
	"github.com/meltforce/homelib/internal/server"
	"github.com/meltforce/homelib/internal/store"
	"tailscale.com/client/tailscale"
	"tailscale.com/tsnet"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	localMode := flag.Bool("local", false, "Run on localhost instead of tsnet (for development)")
	localAddr := flag.String("addr", ":8080", "Listen address in local mode")
	flag.Parse()

	// Logging
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if cfg.Service.LogLevel == "debug" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.Service.DataDir, 0o755); err != nil {
		log.Error("create data dir", "error", err)
		os.Exit(1)
	}

	// Open store
	st, err := store.New(cfg.Service.DataDir)
	if err != nil {
		log.Error("open store", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Start tsnet / get listener (moved before collectors so setec can use tsnet dial)
	var ln net.Listener
	var localClient *tailscale.LocalClient
	var tsnetHTTPClient *http.Client

	if *localMode {
		// Local development mode
		ln, err = net.Listen("tcp", *localAddr)
		if err != nil {
			log.Error("listen", "error", err)
			os.Exit(1)
		}
		log.Info("listening (local mode)", "addr", *localAddr)
		tsnetHTTPClient = http.DefaultClient
	} else {
		// tsnet mode
		tsnetSrv := &tsnet.Server{
			Hostname: cfg.Service.Hostname,
			Dir:      cfg.Service.StateDir,
		}

		// Set auth key if available (from env var, not in setec)
		if authKey, err := cfg.ResolveSecret("ts_auth_key"); err == nil && authKey != "" {
			tsnetSrv.AuthKey = authKey
		}

		if err := tsnetSrv.Start(); err != nil {
			log.Error("tsnet start", "error", err)
			os.Exit(1)
		}
		defer tsnetSrv.Close()

		localClient, err = tsnetSrv.LocalClient()
		if err != nil {
			log.Error("tsnet local client", "error", err)
			os.Exit(1)
		}

		tsnetHTTPClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return tsnetSrv.Dial(ctx, network, addr)
				},
			},
		}

		ln, err = tsnetSrv.ListenTLS("tcp", ":443")
		if err != nil {
			log.Error("tsnet listen", "error", err)
			os.Exit(1)
		}
		log.Info("listening on tsnet", "hostname", cfg.Service.Hostname)
	}

	// Init setec store (uses tsnet HTTP client to reach setec over Tailscale)
	if err := cfg.InitSetecStore(context.Background(), tsnetHTTPClient); err != nil {
		log.Error("init setec store", "error", err)
		os.Exit(1)
	}

	// Create orchestrator
	orch := collector.NewOrchestrator(cfg, st, log)

	// Register collectors based on config
	if cfg.Collectors.Proxmox.Enabled {
		orch.Register(collector.NewProxmoxCollector(cfg.Collectors.Proxmox, log))
	}
	if cfg.Collectors.Hetzner.Enabled {
		orch.Register(collector.NewHetznerCollector(cfg.Collectors.Hetzner, cfg, log))
	}
	if cfg.Collectors.Komodo.Enabled {
		orch.Register(collector.NewKomodoCollector(cfg.Collectors.Komodo, cfg, log))
	}
	if cfg.Collectors.UniFi.Enabled {
		orch.Register(collector.NewUniFiCollector(cfg.Collectors.UniFi, cfg, log))
	}
	if cfg.Collectors.Tailscale.Enabled {
		if localClient != nil {
			orch.Register(collector.NewTailscaleCollector(cfg.Collectors.Tailscale, cfg, localClient, log))
		} else if *localMode {
			log.Warn("tailscale collector disabled in local mode (no tsnet)")
		}
	}

	// Register plugins
	for _, pcfg := range cfg.Plugins {
		if pcfg.Enabled {
			orch.Register(collector.NewPluginCollector(pcfg, log))
		}
	}

	// Build HTTP handler
	srv, err := server.New(st, orch, log)
	if err != nil {
		log.Error("create server", "error", err)
		os.Exit(1)
	}

	// MCP handler
	mcpHandler, err := mcpserver.NewHandler(st, orch)
	if err != nil {
		log.Error("create MCP handler", "error", err)
		os.Exit(1)
	}

	// Combine handlers
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/", srv.Handler())

	// Start scheduler
	sched := scheduler.New(log)
	if cfg.Schedule.Cron != "" {
		if err := sched.Schedule(cfg.Schedule.Cron, func(ctx context.Context) error {
			return orch.Run(ctx)
		}); err != nil {
			log.Error("schedule", "error", err)
			os.Exit(1)
		}
		sched.Start()
		defer sched.Stop()
	}

	// Run initial collection
	go func() {
		log.Info("running initial collection")
		if err := orch.Run(context.Background()); err != nil {
			log.Error("initial collection failed", "error", err)
		}
	}()

	// Serve
	httpSrv := &http.Server{Handler: mux}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("serve", "error", err)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Info("shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10_000_000_000) // 10s
	defer cancel()
	httpSrv.Shutdown(ctx)

	log.Info("goodbye")
}
