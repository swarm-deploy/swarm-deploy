package alertmanagement

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

// Module contains Alert Management services exposed to application entrypoints.
type Module struct {
	// Store persists and queries alerts.
	Store *modelstore.FileStore
	// Subscriber consumes deployment lifecycle events.
	Subscriber *Subscriber
}

// Container supplies dependencies required by Alert Management.
type Container interface {
	// GetFileSystem returns the application filesystem abstraction.
	GetFileSystem() fs.FileSystem
	// GetEventModule returns the event dispatcher module.
	GetEventModule() *event.Module
}

// InitModule initializes persistent alert state and event subscriptions.
func InitModule(ctx context.Context, cfg *config.Config, container Container) (*Module, error) {
	store, err := modelstore.NewFileStore(
		ctx,
		filepath.Join(cfg.Spec.DataDir, "alerts.state.json"),
		container.GetFileSystem(),
	)
	if err != nil {
		return nil, fmt.Errorf("init alert store: %w", err)
	}
	subscriber := NewSubscriber(store)
	module := &Module{Store: store, Subscriber: subscriber}
	dispatcher := container.GetEventModule().Dispatcher
	dispatcher.Subscribe(events.TypeDeployFailed, subscriber)
	dispatcher.Subscribe(events.TypeDeploySuccess, subscriber)
	return module, nil
}
