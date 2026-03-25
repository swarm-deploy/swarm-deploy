package metrics

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/event/events"
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

func (s *Subscriber) Handle(_ context.Context, event events.Event) error {
	s.recorder.IncTotal(event.Type())

	return nil
}
