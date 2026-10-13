package configstore

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ConfigStore reloads the shared configuration and signals effective changes.
type ConfigStore struct {
	cfg               *config.Config
	events            dispatcher.Dispatcher
	initialReloadDone atomic.Bool
}

// New creates a configuration store.
func New(cfg *config.Config, eventsDispatcher dispatcher.Dispatcher) *ConfigStore {
	return &ConfigStore{cfg: cfg, events: eventsDispatcher}
}

// Reload reloads configuration and publishes a signal after the first successful
// reload and whenever the effective state changes afterward.
func (s *ConfigStore) Reload(ctx context.Context, baseDirs ...string) error {
	changed, err := s.cfg.Reload(baseDirs...)
	if err != nil {
		return err
	}
	firstSuccessfulReload := s.initialReloadDone.CompareAndSwap(false, true)
	if !changed && !firstSuccessfulReload {
		return nil
	}
	if err = s.events.Publish(ctx, &events.ConfigurationChanged{}); err != nil {
		slog.ErrorContext(ctx, "publish configuration changed event", slog.Any("err", err))
	}
	return nil
}

// Stacks returns an isolated copy of the currently accepted stacks.
func (s *ConfigStore) Stacks() []config.StackSpec { return s.cfg.Stacks() }

// Networks returns an isolated copy of the currently accepted networks.
func (s *ConfigStore) Networks() []config.NetworkSpec { return s.cfg.Networks() }
