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
	switch e := event.Event.(type) {
	case *events.SendNotificationFailed:
		return s.handleNotificationFailed(ctx, event, e)
	case *events.NotificationSent:
		return s.handleNotificationSent(ctx, event, e)
	}

	resource, handled := orderingResource(event.Event)
	if !handled {
		return nil
	}
	return s.store.ConsumeLatest(ctx, event.ID, resource, event.OccurredAt, func(ctx context.Context) error {
		return s.Subscriber.Handle(ctx, event)
	})
}

func (s *SQLSubscriber) handleNotificationFailed(
	ctx context.Context,
	event events.Envelope,
	failure *events.SendNotificationFailed,
) error {
	key := notificationDestinationKey(failure.DestinationKey, failure.Destination, failure.Channel)
	recoveredAt, err := s.store.NotificationRecoverySequence(ctx, key)
	if err != nil {
		return err
	}
	if event.PublicationSequence <= recoveredAt {
		return nil
	}
	return s.Subscriber.Handle(ctx, event)
}

func (s *SQLSubscriber) handleNotificationSent(
	ctx context.Context,
	event events.Envelope,
	sent *events.NotificationSent,
) error {
	key := notificationDestinationKey(sent.DestinationKey, sent.Destination, sent.Channel)
	if !sent.DeliveredAt.IsZero() {
		alert, err := s.store.FindOpenByFingerprint(ctx, model.NotificationDeliveryFailedFingerprint(key))
		if err != nil {
			if errors.Is(err, modelstore.ErrAlertNotFound) {
				return nil
			}
			return err
		}
		if !alert.LatestEventAt.IsZero() && !sent.DeliveredAt.After(alert.LatestEventAt) {
			return nil
		}
	}
	advanced, err := s.store.AdvanceNotificationRecovery(ctx, key, event.PublicationSequence, event.ID)
	if err != nil || !advanced {
		return err
	}
	return s.Subscriber.Handle(ctx, event)
}

func orderingResource(event events.Event) (string, bool) {
	switch e := event.(type) {
	case *events.ConfigurationChanged:
		if e.PreviousRepository != "" {
			return "repository:" + e.PreviousRepository, true
		}
		return "configuration", true
	case *events.DeploySuccess:
		return "stack:" + e.StackName, true
	case *events.DeployFailed:
		return "stack:" + e.StackName, true
	case *events.DeployPreparationFailed:
		return "stack:" + e.StackName, true
	case *events.DeployInterrupted:
		return "stack:" + e.StackName, true
	case *events.NodeDisconnected:
		return "node:" + e.NodeID, true
	case *events.NodeConnected:
		return "node:" + e.NodeID, true
	case *events.GitPullFailed:
		return "repository:" + e.Repository, true
	case *events.GitPullSucceeded:
		return "repository:" + e.Repository, true
	default:
		return "", false
	}
}
