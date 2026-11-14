package dispatcher

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=subscriber.go -destination=mocks_subscriber_test.go -package=dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
)

// OutboxDispatcher registers subscribers and coordinates durable event delivery.
type OutboxDispatcher struct {
	repository *outbox.Repository
	worker     *outbox.Worker
	mu         sync.RWMutex
	registry   map[events.TypeName]map[string]Subscriber
	started    bool
	now        func() time.Time
}

// NewOutboxDispatcher constructs the dispatcher. Register all subscriptions before starting workers.
func NewOutboxDispatcher(repository *outbox.Repository) *OutboxDispatcher {
	b := &OutboxDispatcher{
		repository: repository,
		registry:   make(map[events.TypeName]map[string]Subscriber),
		now:        time.Now,
	}
	b.worker = outbox.NewWorker(repository, b.resolve, outbox.WithClock(func() time.Time { return b.now() }))
	return b
}

// Subscribe registers a stable destination identity for an event type.
func (b *OutboxDispatcher) Subscribe(typ events.TypeName, id string, handler Subscriber) error {
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

// Publish persists work without invoking subscribers.
func (b *OutboxDispatcher) Publish(ctx context.Context, event events.Event) error {
	if event == nil {
		return errors.New("cannot publish a nil event")
	}
	b.mu.RLock()
	ids := make([]string, 0, len(b.registry[event.Type().Name()]))
	for id := range b.registry[event.Type().Name()] {
		ids = append(ids, id)
	}
	b.mu.RUnlock()
	return b.repository.Publish(ctx, event, ids, b.now())
}

func (b *OutboxDispatcher) resolve(typ events.TypeName, id string) (outbox.DeliveryHandler, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handler := b.registry[typ][id]
	if handler == nil {
		return outbox.DeliveryHandler{}, false
	}
	if external, ok := handler.(externalSubscriber); ok {
		return outbox.DeliveryHandler{Handle: external.Handle, External: true}, true
	}
	return outbox.DeliveryHandler{Handle: handler.Handle}, true
}

// Run processes committed work until cancellation.
func (b *OutboxDispatcher) Run(ctx context.Context) error {
	b.markStarted()
	return b.worker.Run(ctx)
}

// ProcessNext claims and processes at most one delivery.
func (b *OutboxDispatcher) ProcessNext(ctx context.Context) (bool, error) {
	b.markStarted()
	return b.worker.ProcessNext(ctx)
}

func (b *OutboxDispatcher) markStarted() {
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()
}

// Describe implements prometheus.Collector.
func (b *OutboxDispatcher) Describe(ch chan<- *prometheus.Desc) { b.repository.Describe(ch) }

// Collect implements prometheus.Collector.
func (b *OutboxDispatcher) Collect(ch chan<- prometheus.Metric) { b.repository.Collect(ch) }
