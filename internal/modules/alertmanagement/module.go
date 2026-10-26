package alertmanagement

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	eventdispatcher "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodelstore "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
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
	// GetConfigStore returns the shared runtime configuration store.
	GetConfigStore() *configstore.ConfigStore
}

// InitModule initializes persistent alert state and event subscriptions.
func InitModule(ctx context.Context, cfg *config.Config, container Container) (*Module, error) {
	store := modelstore.NewSQLStore(container.GetStorage())
	dispatcher := container.GetEventModule().Dispatcher
	subscriber := NewSQLSubscriber(store, dispatcher, func(kind model.AlertKind) []events.AlertRecipient {
		return cfg.Spec.Notifications.AlertRecipients(string(kind))
	}, container.GetConfigStore(), gitopsmodelstore.NewSQLStore(container.GetStorage()))
	module := &Module{Store: store, Subscriber: subscriber}
	if err := eventdispatcher.SubscribeMany(dispatcher, []events.TypeName{
		events.TypeNameConfigurationChanged,
		events.TypeNameDeployFailed, events.TypeNameDeployPreparationFailed,
		events.TypeNameDeployInterrupted,
		events.TypeNameNodeDisconnected, events.TypeNameNodeConnected,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded,
		events.TypeNameServiceMissed, events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
	}, "alert-management", subscriber); err != nil {
		return nil, err
	}
	if err := dispatcher.Subscribe(events.TypeNameDeploySuccess, "alert-management",
		deployment.WithDesired(deploymentstore.NewStore(container.GetStorage()), subscriber)); err != nil {
		return nil, err
	}
	return module, nil
}
