package scheduler

import (
	"context"
	"log/slog"

	"github.com/robfig/cron/v3"
)

// Scheduler wraps cron for periodic collection runs.
type Scheduler struct {
	cron *cron.Cron
	log  *slog.Logger
}

// New creates a scheduler.
func New(log *slog.Logger) *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithParser(cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
		))),
		log: log,
	}
}

// Schedule adds a cron job. The fn is called on each schedule tick.
func (s *Scheduler) Schedule(spec string, fn func(ctx context.Context) error) error {
	_, err := s.cron.AddFunc(spec, func() {
		s.log.Info("scheduled collection starting", "spec", spec)
		if err := fn(context.Background()); err != nil {
			s.log.Error("scheduled collection failed", "error", err)
		}
	})
	return err
}

// Start begins the scheduler.
func (s *Scheduler) Start() {
	s.cron.Start()
	s.log.Info("scheduler started")
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	s.cron.Stop()
}
