// Package scheduler serializes GitOps reconciliation triggers and periodic work.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"go.opentelemetry.io/otel/trace"
)

const queueCapacity = 1

// Runner executes scheduled GitOps work serially.
type Runner interface {
	// Run executes one complete synchronization.
	Run(ctx context.Context, task syncrun.Task)
	// Poll performs one Git poll and synchronizes an updated revision.
	Poll(ctx context.Context)
	// Cleanup performs scheduled rotated-resource cleanup.
	Cleanup(ctx context.Context)
}

// Scheduler owns the reconciliation queue and all GitOps periodic triggers.
type Scheduler struct {
	cfg      *config.Config
	runner   Runner
	event    dispatcher.Dispatcher
	queue    chan syncrun.Task
	shutdown atomic.Bool
	active   atomic.Bool
	tickerMu sync.Mutex
	tickers  []*time.Ticker
}

// New creates a scheduler preserving the single-slot, non-blocking queue.
func New(cfg *config.Config, syncRunner Runner, eventDispatcher dispatcher.Dispatcher) *Scheduler {
	return &Scheduler{cfg: cfg, runner: syncRunner, event: eventDispatcher, queue: make(chan syncrun.Task, queueCapacity)}
}

// Run starts periodic triggers and processes queued tasks serially.
func (s *Scheduler) Run(ctx, reconciliationCtx context.Context) error {
	s.active.Store(true)
	defer s.active.Store(false)

	var pollTicker *time.Ticker
	if s.cfg.Spec.Sync.Mode == config.SyncModePull || s.cfg.Spec.Sync.Mode == config.SyncModeHybrid {
		pollTicker = time.NewTicker(s.cfg.Spec.Sync.PollInterval.Value)
		s.addTicker(pollTicker)
		defer s.removeTicker(pollTicker)
	}
	reconcileTicker := time.NewTicker(s.cfg.Spec.Sync.Interval.Value)
	s.addTicker(reconcileTicker)
	defer s.removeTicker(reconcileTicker)
	cleanupTicker, stopCleanupTicker := s.newCleanupTicker()
	defer stopCleanupTicker()

	slog.InfoContext(ctx, "[controller] trigger startup sync")
	s.schedule(ctx, syncrun.Task{Reason: syncrun.TriggerStartup})
	for {
		if s.shutdown.Load() {
			return nil
		}
		select {
		case <-ctx.Done():
			s.Shutdown()
			return nil
		case task := <-s.queue:
			if !s.shutdown.Load() {
				s.runner.Run(reconciliationCtx, task)
			}
		case <-tickerC(pollTicker):
			if !s.shutdown.Load() {
				s.runner.Poll(reconciliationCtx)
			}
		case <-reconcileTicker.C:
			if !s.shutdown.Load() {
				s.schedule(ctx, syncrun.Task{Reason: syncrun.TriggerInterval})
			}
		case <-tickerC(cleanupTicker):
			if !s.shutdown.Load() {
				s.runner.Cleanup(reconciliationCtx)
			}
		}
	}
}

// Manual queues a manual synchronization when the scheduler is active.
func (s *Scheduler) Manual(ctx context.Context) bool {
	user, _ := security.UserFromContext(ctx)
	return s.schedule(ctx, syncrun.Task{TriggeredBy: user.Name, Reason: syncrun.TriggerManual})
}

// Webhook queues a webhook synchronization and publishes the existing receipt event.
func (s *Scheduler) Webhook(ctx context.Context) (bool, error) {
	queued := s.schedule(ctx, syncrun.Task{Reason: syncrun.TriggerWebhook})
	return queued, s.event.Publish(ctx, &events.WebhookReceived{Queued: queued})
}

// Shutdown rejects new work and stops periodic triggers.
func (s *Scheduler) Shutdown() {
	s.shutdown.Store(true)
	s.stopTickers()
}

func (s *Scheduler) schedule(ctx context.Context, task syncrun.Task) bool {
	if s.shutdown.Load() || !s.active.Load() {
		return false
	}
	task.SpanContext = trace.SpanContextFromContext(ctx)
	select {
	case s.queue <- task:
		return true
	default:
		return false
	}
}

func (s *Scheduler) newCleanupTicker() (*time.Ticker, func()) {
	if !s.cfg.Spec.SecretRotation.Cleanup.Enabled {
		return nil, func() {}
	}
	ticker := time.NewTicker(s.cfg.Spec.SecretRotation.Cleanup.Interval.Value)
	s.addTicker(ticker)
	return ticker, func() { s.removeTicker(ticker) }
}

func (s *Scheduler) addTicker(ticker *time.Ticker) {
	s.tickerMu.Lock()
	defer s.tickerMu.Unlock()
	s.tickers = append(s.tickers, ticker)
	if s.shutdown.Load() {
		ticker.Stop()
	}
}

func (s *Scheduler) removeTicker(ticker *time.Ticker) {
	s.tickerMu.Lock()
	defer s.tickerMu.Unlock()
	ticker.Stop()
	for index, registered := range s.tickers {
		if registered == ticker {
			s.tickers = append(s.tickers[:index], s.tickers[index+1:]...)
			return
		}
	}
}

func (s *Scheduler) stopTickers() {
	s.tickerMu.Lock()
	defer s.tickerMu.Unlock()
	for _, ticker := range s.tickers {
		ticker.Stop()
	}
}

func tickerC(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}
