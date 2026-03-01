package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
	"github.com/meltforce/homelib/internal/store"
	"golang.org/x/sync/errgroup"
)

// Collector is the interface all data sources must implement.
type Collector interface {
	// Name returns the collector's identifier (e.g. "proxmox", "tailscale").
	Name() string
	// SourceType returns "native" or "plugin".
	SourceType() string
	// Collect gathers data from the source.
	Collect(ctx context.Context) (*model.CollectionResult, error)
}

// Progress tracks the current state of a collection run.
type Progress struct {
	mu       sync.RWMutex
	RunID    int64
	Status   string // running, completed, failed
	Sources  map[string]SourceProgress
	Started  time.Time
	Finished *time.Time
}

// SourceProgress tracks a single source within a run.
type SourceProgress struct {
	Status    string
	StartedAt time.Time
	Error     string
}

// ProgressSnapshot is a lock-free copy of Progress for reading.
type ProgressSnapshot struct {
	RunID    int64
	Status   string
	Sources  map[string]SourceProgress
	Started  time.Time
	Finished *time.Time
}

// GetProgress returns a snapshot of the current progress.
func (p *Progress) GetProgress() ProgressSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snap := ProgressSnapshot{
		RunID:    p.RunID,
		Status:   p.Status,
		Started:  p.Started,
		Finished: p.Finished,
		Sources:  make(map[string]SourceProgress, len(p.Sources)),
	}
	for k, v := range p.Sources {
		snap.Sources[k] = v
	}
	return snap
}

func (p *Progress) setSourceStatus(name, status string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sp := p.Sources[name]
	sp.Status = status
	if err != nil {
		sp.Error = err.Error()
	}
	p.Sources[name] = sp
}

func (p *Progress) startSource(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Sources[name] = SourceProgress{Status: "running", StartedAt: time.Now()}
}

// Orchestrator manages collector execution.
type Orchestrator struct {
	cfg        *config.Config
	store      *store.Store
	collectors []Collector
	progress   *Progress
	log        *slog.Logger
}

// NewOrchestrator creates a new orchestrator with the given config and store.
func NewOrchestrator(cfg *config.Config, st *store.Store, log *slog.Logger) *Orchestrator {
	return &Orchestrator{
		cfg:   cfg,
		store: st,
		log:   log,
		progress: &Progress{
			Sources: make(map[string]SourceProgress),
		},
	}
}

// Register adds a collector to the orchestrator.
func (o *Orchestrator) Register(c Collector) {
	o.collectors = append(o.collectors, c)
}

// CurrentProgress returns the current run progress.
func (o *Orchestrator) CurrentProgress() ProgressSnapshot {
	return o.progress.GetProgress()
}

// Run executes all registered collectors concurrently and stores results.
func (o *Orchestrator) Run(ctx context.Context) error {
	runID, err := o.store.StartRun()
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}

	o.progress.mu.Lock()
	o.progress.RunID = runID
	o.progress.Status = "running"
	o.progress.Started = time.Now()
	o.progress.Finished = nil
	o.progress.Sources = make(map[string]SourceProgress)
	o.progress.mu.Unlock()

	o.log.Info("starting collection run", "run_id", runID, "collectors", len(o.collectors))

	type result struct {
		name   string
		result *model.CollectionResult
	}

	var (
		mu      sync.Mutex
		results []result
	)

	g, gctx := errgroup.WithContext(ctx)

	for _, c := range o.collectors {
		c := c
		g.Go(func() error {
			name := c.Name()
			o.progress.startSource(name)

			srcID, err := o.store.StartSource(runID, name, c.SourceType())
			if err != nil {
				o.log.Error("failed to track source start", "source", name, "error", err)
			}

			o.log.Info("collecting", "source", name)
			start := time.Now()

			res, err := c.Collect(gctx)
			duration := time.Since(start)

			if err != nil {
				o.log.Error("collection failed", "source", name, "error", err, "duration", duration)
				o.progress.setSourceStatus(name, "failed", err)
				if srcID > 0 {
					o.store.FinishSource(srcID, "failed", 0, err.Error())
				}
				// Don't fail the entire run — graceful degradation
				return nil
			}

			itemCount := len(res.Hosts) + len(res.Services) + len(res.Networks) + len(res.Findings)
			o.log.Info("collection complete", "source", name, "items", itemCount, "duration", duration)
			o.progress.setSourceStatus(name, "completed", nil)

			if srcID > 0 {
				o.store.FinishSource(srcID, "completed", itemCount, "")
			}

			mu.Lock()
			results = append(results, result{name: name, result: res})
			mu.Unlock()

			return nil
		})
	}

	// Wait for all collectors — errors are handled per-collector
	g.Wait()

	// Store all results
	for _, r := range results {
		if err := o.storeResult(runID, r.result); err != nil {
			o.log.Error("failed to store result", "source", r.name, "error", err)
		}
	}

	// Purge old runs
	if o.cfg.Schedule.RetentionDays > 0 {
		purged, err := o.store.PurgeOldRuns(o.cfg.Schedule.RetentionDays)
		if err != nil {
			o.log.Warn("failed to purge old runs", "error", err)
		} else if purged > 0 {
			o.log.Info("purged old runs", "count", purged)
		}
	}

	status := "completed"
	if err := o.store.FinishRun(runID, status, nil); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}

	now := time.Now()
	o.progress.mu.Lock()
	o.progress.Status = status
	o.progress.Finished = &now
	o.progress.mu.Unlock()

	o.log.Info("collection run complete", "run_id", runID)
	return nil
}

func (o *Orchestrator) storeResult(runID int64, res *model.CollectionResult) error {
	if len(res.Hosts) > 0 {
		if err := o.store.InsertHosts(runID, res.Hosts); err != nil {
			return fmt.Errorf("store hosts: %w", err)
		}
	}
	if len(res.Services) > 0 {
		if err := o.store.InsertServices(runID, res.Services); err != nil {
			return fmt.Errorf("store services: %w", err)
		}
	}
	if len(res.Networks) > 0 {
		if err := o.store.InsertNetworks(runID, res.Networks); err != nil {
			return fmt.Errorf("store networks: %w", err)
		}
	}
	if len(res.Firewalls) > 0 {
		if err := o.store.InsertFirewalls(runID, res.Firewalls); err != nil {
			return fmt.Errorf("store firewalls: %w", err)
		}
	}
	if res.ACL != nil {
		if err := o.store.InsertTailscaleACL(runID, res.ACL); err != nil {
			return fmt.Errorf("store tailscale ACL: %w", err)
		}
	}
	if res.DNS != nil {
		if err := o.store.InsertTailscaleDNS(runID, res.DNS); err != nil {
			return fmt.Errorf("store tailscale DNS: %w", err)
		}
	}
	if len(res.Routes) > 0 {
		if err := o.store.InsertTailscaleRoutes(runID, res.Routes); err != nil {
			return fmt.Errorf("store tailscale routes: %w", err)
		}
	}
	if len(res.Keys) > 0 {
		if err := o.store.InsertTailscaleKeys(runID, res.Keys); err != nil {
			return fmt.Errorf("store tailscale keys: %w", err)
		}
	}
	if res.PluginMetrics != nil {
		if err := o.store.InsertPluginMetrics(runID, res.PluginMetrics); err != nil {
			return fmt.Errorf("store plugin metrics: %w", err)
		}
	}
	if len(res.Findings) > 0 {
		if err := o.store.InsertFindings(runID, res.Findings); err != nil {
			return fmt.Errorf("store findings: %w", err)
		}
	}
	return nil
}
