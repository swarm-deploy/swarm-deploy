package dispatcher

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"go.opentelemetry.io/otel/trace"
)

const queueSize = 200

type message struct {
	Event       events.Event
	Subscriber  Subscriber
	SpanContext trace.SpanContext
}

type scheduledMessage struct {
	Event       events.Event
	SpanContext trace.SpanContext
}

type queue struct {
	name   string
	queue  chan *message
	sender EventSender
}

func newQueue(name string) *queue {
	q := &queue{
		name:   name,
		queue:  make(chan *message, queueSize),
		sender: createEventSender(),
	}

	go func() {
		q.runWorker()
	}()

	return q
}

func (q *queue) Name() string {
	return q.name
}

func (q *queue) Dispatch(task *message) {
	q.queue <- task
}

func (q *queue) Close() {
	close(q.queue)
}

func (q *queue) runWorker() {
	ctx := context.Background()

	for task := range q.queue {
		_ = q.sender(ctx, *task)
	}
}
