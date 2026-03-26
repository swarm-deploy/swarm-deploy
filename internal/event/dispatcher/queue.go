package dispatcher

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/artarts36/swarm-deploy/internal/event/logx"
	"github.com/artarts36/swarm-deploy/internal/security"
)

const (
	defaultEventsQueueLen         = 128
	defaultSubscribeHandleTimeout = 5 * time.Minute
)

type QueueDispatcher struct {
	subscribers map[events.Type][]Subscriber

	now   func() time.Time
	queue chan events.Event

	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

func NewQueueDispatcher() *QueueDispatcher {
	d := &QueueDispatcher{
		now:         time.Now,
		queue:       make(chan events.Event, defaultEventsQueueLen),
		subscribers: map[events.Type][]Subscriber{},
	}

	d.wg.Add(1)
	go d.runWorker()

	return d
}

func (d *QueueDispatcher) Dispatch(ctx context.Context, event events.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		slog.InfoContext(ctx, "[event] event not dispatched, channel closed", slog.Any("event", event))

		return
	}

	eventAwareUser, ok := event.(events.AwareUser)
	if ok {
		user, uok := security.UserFromContext(ctx)
		if uok {
			event = eventAwareUser.WithUsername(user.Name)
		}
	}

	slog.InfoContext(ctx, "[event] dispatching event", slog.Any("event", event),
		slog.String("event.type", string(event.Type())),
	)

	d.queue <- event
}

// Subscribe registers a subscriber for event type.
func (d *QueueDispatcher) Subscribe(eventType events.Type, subscriber Subscriber) {
	d.mu.Lock()
	d.subscribers[eventType] = append(d.subscribers[eventType], subscriber)
	d.mu.Unlock()
}

func (d *QueueDispatcher) runWorker() {
	defer d.wg.Done()

	handle := func(subscriber Subscriber, event events.Event) {
		ctx, cancel := context.WithTimeout(context.Background(), defaultSubscribeHandleTimeout)
		defer cancel()

		ctx = logx.ContextWithEventType(ctx, event.Type())

		slog.DebugContext(ctx, "[event] running subscriber",
			slog.String("subscriber.name", subscriber.Name()),
		)

		err := subscriber.Handle(context.Background(), event)
		if err != nil {
			slog.WarnContext(ctx, "[event] subscriber failed", slog.Any("err", err))
		}
	}

	for event := range d.queue {
		d.mu.RLock()
		subscribers := append([]Subscriber{}, d.subscribers[event.Type()]...)
		d.mu.RUnlock()

		for _, subscriber := range subscribers {
			handle(subscriber, event)
		}
	}
}

func (d *QueueDispatcher) Shutdown(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("dispatcher already shut down")
	}
	d.closed = true
	close(d.queue)
	d.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		return nil
	case <-ctx.Done():
		return errors.Join(errors.New("shutdown dispatcher"), ctx.Err())
	}
}
