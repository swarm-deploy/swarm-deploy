package alertmanagement

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Module contains Alert Management services exposed to application entrypoints.
type Module struct {
	// Store persists and queries alerts.
	Store *modelstore.SQLStore
	// Subscriber consumes deployment lifecycle events.
	Subscriber *SQLSubscriber
}

// Container supplies dependencies required by Alert Management.
type Container interface {
	// GetStorage returns the shared database.
	GetStorage() *storage.Database
	// GetFileSystem returns the application filesystem abstraction.
	// GetEventModule returns the event dispatcher module.
	GetEventModule() *event.Module
}

// InitModule initializes persistent alert state and event subscriptions.
func InitModule(ctx context.Context, cfg *config.Config, container Container) (*Module, error) {
	store := modelstore.NewSQLStore(container.GetStorage())
	subscriber := NewSQLSubscriber(store)
	module := &Module{Store: store, Subscriber: subscriber}
	dispatcher := container.GetEventModule().Dispatcher
	for _, typ := range []events.TypeName{
		events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed,
		events.TypeNameDeployInterrupted, events.TypeNameDeploySuccess,
		events.TypeNameNodeDisconnected, events.TypeNameNodeConnected,
	} {
		if err := dispatcher.Subscribe(typ, "alert-management", subscriber); err != nil {
			return nil, err
		}
	}
	return module, nil
}
