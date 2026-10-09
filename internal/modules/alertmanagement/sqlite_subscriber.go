package alertmanagement

import (
	"context"

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
func NewSQLSubscriber(store *modelstore.SQLStore) *SQLSubscriber {
	return &SQLSubscriber{Subscriber: NewSubscriber(store), store: store}
}

// Handle applies each source publication once, even across retry and restart.
func (s *SQLSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	return s.store.Consume(ctx, event.ID, func(ctx context.Context) error {
		return s.Subscriber.Handle(ctx, event)
	})
}
