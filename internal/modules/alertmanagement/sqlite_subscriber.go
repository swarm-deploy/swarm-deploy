package alertmanagement

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// SQLSubscriber joins source deduplication and the alert lifecycle in one transaction.
type SQLSubscriber struct {
	// Subscriber implements the alert lifecycle using the shared repository.
	*Subscriber
	store *modelstore.SQLStore
}

// NewSQLSubscriber constructs the durable alert projection.
func NewSQLSubscriber(store *modelstore.SQLStore, publisher interface {
	Publish(context.Context, events.Event) error
},
	recipients func(model.AlertKind) []events.AlertRecipient,
	stacks config.StackProvider,
) *SQLSubscriber {
	return &SQLSubscriber{Subscriber: NewSubscriber(store, publisher, recipients, stacks), store: store}
}

// Handle applies each source publication once, even across retry and restart.
func (s *SQLSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	var resource string
	switch e := event.Event.(type) {
	case *events.ConfigurationChanged:
		resource = "configuration"
		if e.PreviousRepository != "" {
			resource = "repository:" + e.PreviousRepository
		}
	case *events.DeploySuccess:
		resource = "stack:" + e.StackName
	case *events.DeployFailed:
		resource = "stack:" + e.StackName
	case *events.DeployPreparationFailed:
		resource = "stack:" + e.StackName
	case *events.DeployInterrupted:
		resource = "stack:" + e.StackName
	case *events.NodeDisconnected:
		resource = "node:" + e.NodeID
	case *events.NodeConnected:
		resource = "node:" + e.NodeID
	case *events.GitPullFailed:
		resource = "repository:" + e.Repository
	case *events.GitPullSucceeded:
		resource = "repository:" + e.Repository
	case *events.ServiceMissed:
		resource = "service:" + e.StackName + "/" + e.ServiceName
	case *events.ServiceDrifted:
		resource = "service:" + e.StackName + "/" + e.ServiceName
	case *events.ServiceDriftFixed:
		resource = "service:" + e.StackName + "/" + e.ServiceName
	case *events.ServiceRemovedFromDesired:
		resource = "service:" + e.StackName + "/" + e.ServiceName
	default:
		return nil
	}
	return s.store.ConsumeLatest(ctx, event.ID, resource, event.OccurredAt, func(ctx context.Context) error {
		return s.Subscriber.Handle(ctx, event)
	})
}
