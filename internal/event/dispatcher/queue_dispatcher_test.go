package dispatcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

func TestQueueDispatcher_DeduplicatesByWindow(t *testing.T) {
	dispatcher := NewQueueDispatcher()
	t.Cleanup(func() {
		err := dispatcher.Shutdown(context.Background())
		require.NoError(t, err, "shutdown dispatcher")
	})

	var nowMu sync.Mutex
	now := time.Date(2026, time.April, 25, 10, 0, 0, 0, time.UTC)
	dispatcher.now = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	sub := newCollectSubscriber()
	dispatcher.Subscribe(events.TypeUserAuthenticated, sub)

	event := &events.UserAuthenticated{Username: "alice"}
	dispatcher.Dispatch(context.Background(), event)
	dispatcher.Dispatch(context.Background(), event)

	require.Eventually(t, func() bool {
		return sub.Len() == 1
	}, time.Second, 10*time.Millisecond, "expected only first event to be dispatched")
}

func TestQueueDispatcher_AllowsDispatchAfterWindow(t *testing.T) {
	dispatcher := NewQueueDispatcher()
	t.Cleanup(func() {
		err := dispatcher.Shutdown(context.Background())
		require.NoError(t, err, "shutdown dispatcher")
	})

	var nowMu sync.Mutex
	now := time.Date(2026, time.April, 25, 10, 0, 0, 0, time.UTC)
	dispatcher.now = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}

	sub := newCollectSubscriber()
	dispatcher.Subscribe(events.TypeUserAuthenticated, sub)

	event := &events.UserAuthenticated{Username: "alice"}
	dispatcher.Dispatch(context.Background(), event)
	require.Eventually(t, func() bool {
		return sub.Len() == 1
	}, time.Second, 10*time.Millisecond, "expected first event to be dispatched")

	nowMu.Lock()
	now = now.Add(events.TypeUserAuthenticated.Window() + time.Second)
	nowMu.Unlock()

	dispatcher.Dispatch(context.Background(), event)

	require.Eventually(t, func() bool {
		return sub.Len() == 2
	}, time.Second, 10*time.Millisecond, "expected two events after window elapsed")
}

func TestQueueDispatcher_DifferentDetailsNotDeduplicated(t *testing.T) {
	dispatcher := NewQueueDispatcher()
	t.Cleanup(func() {
		err := dispatcher.Shutdown(context.Background())
		require.NoError(t, err, "shutdown dispatcher")
	})

	sub := newCollectSubscriber()
	dispatcher.Subscribe(events.TypeUserAuthenticated, sub)

	dispatcher.Dispatch(context.Background(), &events.UserAuthenticated{Username: "alice"})
	dispatcher.Dispatch(context.Background(), &events.UserAuthenticated{Username: "bob"})

	require.Eventually(t, func() bool {
		return sub.Len() == 2
	}, time.Second, 10*time.Millisecond, "expected both events with different details to be dispatched")
	assert.Equal(t, []string{"alice", "bob"}, sub.Usernames(), "expected original event order")
}

type collectSubscriber struct {
	mu     sync.Mutex
	events []events.Event
}

func newCollectSubscriber() *collectSubscriber {
	return &collectSubscriber{events: make([]events.Event, 0, 2)}
}

func (s *collectSubscriber) Name() string {
	return "collect-subscriber"
}

func (s *collectSubscriber) Slow() bool {
	return false
}

func (s *collectSubscriber) Handle(_ context.Context, event events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *collectSubscriber) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *collectSubscriber) Usernames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.events))
	for _, event := range s.events {
		auth, ok := event.(*events.UserAuthenticated)
		if !ok {
			continue
		}

		out = append(out, auth.Username)
	}

	return out
}
