package event

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	eventmetrics "github.com/swarm-deploy/swarm-deploy/internal/modules/event/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

type Module struct {
	// Dispatcher publishes events and registers subscribers.
	Dispatcher dispatcher.Dispatcher
	// History stores the retained event history.
	History *history.SQLStore

	// Bus is the only durable delivery worker.
	Bus *dispatcher.OutboxDispatcher
}

type Container interface {
	// GetStorage returns the shared database.
	GetStorage() storage.Database
	// GetMetrics returns the application's metrics group.
	GetMetrics() *metrics.Group
}

func InitModule(historyCapacity int, enrichUser bool, cnt Container) (*Module, error) {
	db := cnt.GetStorage()
	historyStore, err := history.NewSQLStore(db, historyCapacity)
	if err != nil {
		return nil, fmt.Errorf("init history store: %w", err)
	}

	bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
	srv := &Module{Dispatcher: bus, History: historyStore, Bus: bus}

	if enrichUser {
		srv.Dispatcher = dispatcher.NewEnrichableDispatcher(
			dispatcher.WrapEnrichers(security.EnrichEvent()),
			srv.Dispatcher,
		)
	}

	if err = dispatcher.SubscribeMany(srv.Dispatcher, projectionEventTypes(), "event-history", historyStore); err != nil {
		return nil, err
	}
	if err = dispatcher.SubscribeMany(srv.Dispatcher, projectionEventTypes(),
		"event-metrics", eventmetrics.NewSubscriber(cnt.GetMetrics().Events)); err != nil {
		return nil, err
	}

	return srv, nil
}

// Run processes durable deliveries until cancellation.
func (s *Module) Run(ctx context.Context) error { return s.Bus.Run(ctx) }

// Keep the original projection subscription set independent of IsPublic.
// History.Handle retains its own persistence filters.
func projectionEventTypes() []events.TypeName {
	return []events.TypeName{
		events.TypeNameServiceCatalogUpdated, events.TypeNameDeploySuccess, events.TypeNameSendNotificationFailed,
		events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived, events.TypeNameNodeJoined,
		events.TypeNameSyncPaused, events.TypeNameSyncResumed, events.TypeNameSyncPauseExpired,
		events.TypeNameNodeConnected, events.TypeNameNodeDisconnected, events.TypeNameServiceMissed,
		events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
		events.TypeNameServiceReplicasIncreased, events.TypeNameServiceReplicasDecreased,
		events.TypeNameServiceRestarted, events.TypeNameServicePruned, events.TypeNameNetworkCreated,
		events.TypeNameUserAuthenticated, events.TypeNameAssistantPromptInjectionDetected,
	}
}
