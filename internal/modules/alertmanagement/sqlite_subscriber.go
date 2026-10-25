package alertmanagement

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// SQLSubscriber applies alert lifecycle transitions inside the Outbox subscriber transaction.
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

// Handle applies an alert lifecycle transition with its ordering guard.
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
	case *events.SendNotificationFailed:
		key := notificationDestinationKey(e.DestinationKey, e.Destination, e.Channel)
		recoveredAt, err := s.store.NotificationRecoverySequence(ctx, key)
		if err != nil {
			return err
		}
		if event.PublicationSequence <= recoveredAt {
			return nil
		}
		return s.Subscriber.Handle(ctx, event)
	case *events.NotificationSent:
		key := notificationDestinationKey(e.DestinationKey, e.Destination, e.Channel)
		if !e.DeliveredAt.IsZero() {
			alert, findErr := s.store.FindOpenByFingerprint(
				ctx, model.NotificationDeliveryFailedFingerprint(key),
			)
			if findErr != nil {
				if errors.Is(findErr, modelstore.ErrAlertNotFound) {
					return nil
				}
				return findErr
			}
			if !alert.LatestEventAt.IsZero() && !e.DeliveredAt.After(alert.LatestEventAt) {
				return nil
			}
		}
		advanced, err := s.store.AdvanceNotificationRecovery(ctx, key, event.PublicationSequence, event.ID)
		if err != nil || !advanced {
			return err
		}
		return s.Subscriber.Handle(ctx, event)
	default:
		return nil
	}
	return s.store.ConsumeLatest(ctx, event.ID, resource, event.OccurredAt, func(ctx context.Context) error {
		return s.Subscriber.Handle(ctx, event)
	})
}
