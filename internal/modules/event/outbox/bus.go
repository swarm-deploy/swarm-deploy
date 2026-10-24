// Package outbox implements durable asynchronous event delivery.
package outbox

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=bus.go -destination=mocks_test.go -package=outbox

import (
	"context"
	"errors"
	"fmt"
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

type monitoredSubscriber struct {
	Subscriber
	destination string
	channel     string
}

type subscription struct {
	handler     Subscriber
	external    bool
	monitor     bool
	destination string
	channel     string
}

// External adapts a network consumer at the worker boundary. Its Handle runs
// without a database transaction; delivery still uses the same durable queue.
func External(handler Subscriber) Subscriber { return externalSubscriber{handler} }

// MonitorDelivery marks a notification transport for terminal delivery monitoring.
// Destination and channel must be stable, non-secret identifiers selected by the caller.
func MonitorDelivery(handler Subscriber, destination, channel string) Subscriber {
	return monitoredSubscriber{Subscriber: handler, destination: destination, channel: channel}
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
	registry    map[events.TypeName]map[string]subscription
	started     bool
	now         func() time.Time
	lease       time.Duration
	maxAttempts int
	workers     int
	tracer      trace.Tracer
}

// New constructs the bus. Register all subscriptions before starting workers.
func New(db *storage.Database) *Bus {
	return &Bus{db: db, registry: make(map[events.TypeName]map[string]subscription),
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
	registered, err := inspectSubscription(handler)
	if err != nil {
		return err
	}
	if b.registry[typ] == nil {
		b.registry[typ] = make(map[string]subscription)
	}
	if _, exists := b.registry[typ][id]; exists {
		return fmt.Errorf("duplicate subscription %q for %q", id, typ)
	}
	b.registry[typ][id] = registered
	return nil
}

func inspectSubscription(handler Subscriber) (subscription, error) {
	registered := subscription{handler: handler}
	for {
		switch wrapped := registered.handler.(type) {
		case externalSubscriber:
			registered.external = true
			registered.handler = wrapped.Subscriber
		case monitoredSubscriber:
			if registered.monitor {
				return subscription{}, errors.New("delivery monitoring can only be configured once")
			}
			if wrapped.destination == "" || wrapped.channel == "" {
				return subscription{}, errors.New("delivery monitoring requires destination and channel")
			}
			registered.monitor = true
			registered.destination, registered.channel = wrapped.destination, wrapped.channel
			registered.handler = wrapped.Subscriber
		default:
			return registered, nil
		}
	}
}

// Publish only enqueues. With an active transaction it joins the publisher's T1
// (or a subscriber's T3); otherwise it opens a short insertion transaction.
func (b *Bus) Publish(ctx context.Context, event events.Event) error {
	if event == nil {
		return errors.New("cannot publish a nil event")
	}
	b.mu.RLock()
	type target struct {
		id          string
		monitor     bool
		destination string
		channel     string
	}
	targets := make([]target, 0, len(b.registry[event.Type().Name()]))
	for id, registered := range b.registry[event.Type().Name()] {
		monitor := registered.monitor && monitorableEventType(event.Type().Name())
		targets = append(targets, target{
			id: id, monitor: monitor, destination: registered.destination, channel: registered.channel,
		})
	}
	b.mu.RUnlock()
	if len(targets) == 0 {
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
		for _, target := range targets {
			query, args, buildErr = goqu.Dialect("sqlite3").Insert("outbox_deliveries").Prepared(true).
				Rows(goqu.Record{
					"event_id": id, "subscription_id": target.id,
					"monitor_delivery": target.monitor, "destination": target.destination, "channel": target.channel,
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

func monitorableEventType(typ events.TypeName) bool {
	switch typ {
	case events.TypeNameAlertOpened, events.TypeNameAlertResolved,
		events.TypeNameOutboxDeliveryFailed, events.TypeNameOutboxDeliveryRecovered,
		events.TypeNameOutboxDeliveryDiscarded:
		return false
	case events.TypeNameServiceCatalogUpdated, events.TypeNameDeploySuccess,
		events.TypeNameGitPullFailed, events.TypeNameGitPullSucceeded,
		events.TypeNameDeployFailed, events.TypeNameConfigurationChanged,
		events.TypeNameDeployPreparationFailed, events.TypeNameDeployInterrupted,
		events.TypeNameSyncManualStarted, events.TypeNameWebhookReceived,
		events.TypeNameNodeJoined, events.TypeNameNodeConnected, events.TypeNameNodeDisconnected,
		events.TypeNameServiceMissed, events.TypeNameServiceReplicasIncreased,
		events.TypeNameServiceReplicasDecreased, events.TypeNameServiceRestarted,
		events.TypeNameServicePruned, events.TypeNameNetworkCreated,
		events.TypeNameUserAuthenticated, events.TypeNameAssistantPromptInjectionDetected:
		return true
	default:
		return false
	}
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
