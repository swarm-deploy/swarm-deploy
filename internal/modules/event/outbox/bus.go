// Package outbox implements durable asynchronous event delivery.
package outbox

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=bus.go -destination=mocks_test.go -package=outbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/google/uuid"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
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

// NotificationSubscriber sends a message and reports whether it performed an external delivery.
type NotificationSubscriber interface {
	HandleNotification(context.Context, events.Envelope) (bool, error)
}

type notificationSubscriber struct {
	handler        NotificationSubscriber
	destination    string
	channel        string
	destinationKey string
}

// Handle implements Subscriber for the Outbox registry.
func (s notificationSubscriber) Handle(ctx context.Context, event events.Envelope) error {
	_, err := s.HandleNotification(ctx, event)
	return err
}

// HandleNotification isolates notification handler panics from the delivery worker.
func (s notificationSubscriber) HandleNotification(
	ctx context.Context, event events.Envelope,
) (delivered bool, err error) {
	defer func() {
		if recover() != nil {
			delivered = false
			err = errors.New("outbox handler panicked")
		}
	}()
	return s.handler.HandleNotification(ctx, event)
}

// External adapts a network consumer at the worker boundary. Its Handle runs
// without a database transaction; delivery still uses the same durable queue.
func External(handler Subscriber) Subscriber { return externalSubscriber{handler} }

// ExternalNotification adapts a network notification consumer and records only
// the safe destination metadata needed for terminal delivery-failure events.
func ExternalNotification(handler NotificationSubscriber, destination, channel, destinationKey string) Subscriber {
	return notificationSubscriber{
		handler: handler, destination: destination, channel: channel, destinationKey: destinationKey,
	}
}

// NotificationDestinationKey returns a stable non-reversible identity for one
// physical recipient. Identity parts must describe transport coordinates, not aliases.
func NotificationDestinationKey(destination string, identityParts ...string) string {
	identity := strings.Join(append([]string{destination}, identityParts...), "\x00")
	return destination + ":" + fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
}

type causeKey struct{}

const (
	defaultLease             = 5 * time.Minute
	defaultMaxAttempts       = 12
	databaseOperationTimeout = 5 * time.Second
	workerPollInterval       = 250 * time.Millisecond
	defaultWorkerCount       = 4
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
	workers     int
	tracer      trace.Tracer
}

// New constructs the bus. Register all subscriptions before starting workers.
func New(db *storage.Database) *Bus {
	return &Bus{db: db, registry: make(map[events.TypeName]map[string]Subscriber),
		now: time.Now, lease: defaultLease, maxAttempts: defaultMaxAttempts, workers: defaultWorkerCount,
		tracer: otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox")}
}

// Subscribe registers a stable destination identity for an event type.
func (b *Bus) Subscribe(typ events.TypeName, id string, handler Subscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return errors.New("subscriptions must be registered before starting workers")
	}
	if _, registered := events.ParseType(string(typ)); !registered {
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
	id, now := eventID.String(), b.now().UTC()
	if now.IsZero() {
		return errors.New("outbox publication time is required")
	}
	traceparent := traceparentFromContext(ctx)
	insert := func(ctx context.Context) error {
		cause, _ := ctx.Value(causeKey{}).(string)
		query, args, buildErr := goqu.Dialect("sqlite3").Insert("outbox_events").Prepared(true).
			Rows(goqu.Record{
				"id": id, "event_type": event.Type().Name(), "schema_version": codec.Version,
				"occurred_at": now, "causation_id": cause, "payload": string(payload),
				"traceparent": traceparent,
			}).ToSQL()
		if buildErr != nil {
			return buildErr
		}
		_, insertErr := b.db.Exec(ctx, query, args...)
		if insertErr != nil {
			return fmt.Errorf("insert outbox event: %w", insertErr)
		}
		for _, subscriber := range ids {
			query, args, buildErr = goqu.Dialect("sqlite3").Insert("outbox_deliveries").Prepared(true).
				Rows(goqu.Record{
					"event_id": id, "subscription_id": subscriber,
					"available_at": now, "updated_at": now,
				}).ToSQL()
			if buildErr != nil {
				return buildErr
			}
			_, insertErr = b.db.Exec(ctx, query, args...)
			if insertErr != nil {
				return fmt.Errorf("insert outbox delivery: %w", insertErr)
			}
		}
		return nil
	}
	return b.db.WithinTransaction(ctx, insert)
}

func traceparentFromContext(ctx context.Context) any {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return nil
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	if traceparent := carrier.Get("traceparent"); traceparent != "" {
		return traceparent
	}
	return nil
}
