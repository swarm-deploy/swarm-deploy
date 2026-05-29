package metrics

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type EventRecorder interface {
	IncTotal(eventType events.Type)
}

type Subscriber struct {
	recorder EventRecorder
}

func NewSubscriber(recorder EventRecorder) *Subscriber {
	return &Subscriber{recorder: recorder}
}

func (*Subscriber) Name() string {
	return "record-event-metrics"
}

func (*Subscriber) Slow() bool {
	return false
}

func (s *Subscriber) Handle(_ context.Context, event events.Event) error {
	s.recorder.IncTotal(event.Type())

	return nil
}
