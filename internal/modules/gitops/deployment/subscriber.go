package deployment

import (
	"context"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
)

type desiredSubscriber struct {
	store *Store
	next  outbox.Subscriber
}

// WithDesired loads redacted effective input by ID rather than embedding Compose in the outbox.
func WithDesired(store *Store, next outbox.Subscriber) outbox.Subscriber {
	return &desiredSubscriber{store: store, next: next}
}

func (s *desiredSubscriber) Handle(ctx context.Context, envelope events.Envelope) error {
	var meta events.DeployEvent
	switch e := envelope.Event.(type) {
	case *events.DeploySuccess:
		meta = e.DeployEvent
	case *events.DeployFailed:
		meta = e.DeployEvent
	default:
		return s.next.Handle(ctx, envelope)
	}
	if meta.DeploymentID == "" {
		return fmt.Errorf("deployment reference missing")
	}
	desired, err := s.store.Desired(ctx, meta.DeploymentID)
	if err != nil {
		return err
	}
	meta.StackDefinition, meta.Services = desired, desired.Compose.Services
	switch e := envelope.Event.(type) {
	case *events.DeploySuccess:
		copied := *e
		copied.DeployEvent = meta
		envelope.Event = &copied
	case *events.DeployFailed:
		copied := *e
		copied.DeployEvent = meta
		envelope.Event = &copied
	}
	return s.next.Handle(ctx, envelope)
}
