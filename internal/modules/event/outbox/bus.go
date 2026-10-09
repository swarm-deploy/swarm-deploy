// Package outbox implements durable asynchronous event delivery.
package outbox

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=bus.go -destination=mocks_test.go -package=outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Thiht/transactor/stdlib"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// Subscriber consumes one durable publication.
type Subscriber interface {
	// Handle applies the event; database repositories use the supplied context.
	Handle(context.Context, events.Envelope) error
}

type externalSubscriber struct{ Subscriber }

// External adapts a network consumer at the worker boundary. Its Handle runs
// without a database transaction; delivery still uses the same durable queue.
func External(handler Subscriber) Subscriber { return externalSubscriber{handler} }

type causeKey struct{}

const (
	defaultLease             = 5 * time.Minute
	defaultMaxAttempts       = 12
	databaseOperationTimeout = 5 * time.Second
	workerPollInterval       = 250 * time.Millisecond
	maxRetryBackoff          = 5 * time.Minute
	maxBackoffExponent       = 9
)

// Bus records publications and independently processes subscriber deliveries.
type Bus struct {
	db          *storage.Database
	mu          sync.RWMutex
	registry    map[events.TypeName]map[string]Subscriber
	started     bool
	now         func() time.Time
	lease       time.Duration
	maxAttempts int
}

// New constructs the bus. Register all subscriptions before starting workers.
func New(db *storage.Database) *Bus {
	return &Bus{db: db, registry: make(map[events.TypeName]map[string]Subscriber),
		now: time.Now, lease: defaultLease, maxAttempts: defaultMaxAttempts}
}

// Subscribe registers a stable destination identity for an event type.
func (b *Bus) Subscribe(typ events.TypeName, id string, handler Subscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return errors.New("subscriptions must be registered before starting workers")
	}
	if _, ok := events.ParseType(string(typ)); !ok {
		return fmt.Errorf("unknown event type %q", typ)
	}
	if id == "" || handler == nil {
		return errors.New("subscription requires an ID and handler")
	}
	if b.registry[typ] == nil {
		b.registry[typ] = make(map[string]Subscriber)
	}
	if _, exists := b.registry[typ][id]; exists {
		return fmt.Errorf("duplicate subscription %q for %q", id, typ)
	}
	b.registry[typ][id] = handler
	return nil
}

// Publish only enqueues. With an active transaction it joins the publisher's T1
// (or a subscriber's T3); otherwise it opens a short insertion transaction.
func (b *Bus) Publish(ctx context.Context, event events.Event) error {
	if event == nil {
		return errors.New("cannot publish a nil event")
	}
	b.mu.RLock()
	ids := make([]string, 0, len(b.registry[event.Type().Name()]))
	for id := range b.registry[event.Type().Name()] {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	if len(ids) == 0 {
		return nil
	}
	payload, err := codec.Encode(event)
	if err != nil {
		return err
	}
	eventID, idErr := uuid.NewV7()
	if idErr != nil {
		return idErr
	}
	id, now := eventID.String(), b.now().UnixMilli()
	insert := func(ctx context.Context) error {
		db := b.db.Get(ctx)
		cause, _ := ctx.Value(causeKey{}).(string)
		_, insertErr := db.ExecContext(ctx, `INSERT INTO outbox_events
			(id,event_type,schema_version,occurred_at_ms,causation_id,payload) VALUES (?,?,?,?,?,?)`,
			id, event.Type().Name(), codec.Version, now, cause, string(payload))
		if insertErr != nil {
			return fmt.Errorf("insert outbox event: %w", insertErr)
		}
		for _, subscriber := range ids {
			_, insertErr = db.ExecContext(ctx, `INSERT INTO outbox_deliveries
				(event_id,subscription_id,available_at_ms,updated_at_ms) VALUES (?,?,?,?)`, id, subscriber, now, now)
			if insertErr != nil {
				return fmt.Errorf("insert outbox delivery: %w", insertErr)
			}
		}
		return nil
	}
	if stdlib.IsWithinTransaction(ctx) {
		return insert(ctx)
	}
	return b.db.WithinTransaction(ctx, insert)
}
