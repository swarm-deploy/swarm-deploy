package history

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreHandlePersistsAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-history.json")

	store, err := NewStore(path, 2)
	require.NoError(t, err, "new store")

	store.now = func() time.Time { return time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC) }
	require.NoError(t, store.Handle(context.Background(), &events.SyncManualStarted{}), "save first event")

	store.now = func() time.Time { return time.Date(2026, 3, 22, 10, 1, 0, 0, time.UTC) }
	require.NoError(
		t,
		store.Handle(context.Background(), &events.DeploySuccess{StackName: "api", Commit: "abc"}),
		"save second event",
	)

	store.now = func() time.Time { return time.Date(2026, 3, 22, 10, 2, 0, 0, time.UTC) }
	require.NoError(
		t,
		store.Handle(context.Background(), &events.DeployFailed{StackName: "api", Commit: "def", Error: errors.New("boom")}),
		"save third event",
	)

	items := store.List()
	require.Len(t, items, 2, "expected rotated history size")
	assert.Equal(t, events.Type(events.TypeDeploySuccess), items[0].Type, "expected middle event")
	assert.Equal(t, events.Type(events.TypeDeployFailed), items[1].Type, "expected newest event")
	assert.Equal(t, "boom", items[1].Details["error"], "expected error text")
	assert.Equal(t, "api", items[1].Details["stack"], "expected stack")
	assert.Equal(t, "def", items[1].Details["commit"], "expected commit")

	reloaded, err := NewStore(path, 2)
	require.NoError(t, err, "reload store")
	reloadedItems := reloaded.List()
	require.Len(t, reloadedItems, 2, "expected persisted rotated history size")
	assert.Equal(t, items, reloadedItems, "expected persisted entries")
}

func TestStoreHandleUserAuthenticated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-history.json")

	store, err := NewStore(path, 10)
	require.NoError(t, err, "new store")

	store.now = func() time.Time { return time.Date(2026, 3, 22, 10, 3, 0, 0, time.UTC) }
	require.NoError(
		t,
		store.Handle(context.Background(), &events.UserAuthenticated{Username: "admin"}),
		"save user authenticated event",
	)

	items := store.List()
	require.Len(t, items, 1, "expected single event")
	assert.Equal(t, events.Type(events.TypeUserAuthenticated), items[0].Type, "expected userAuthenticated type")
	assert.Equal(t, "User admin authenticated", items[0].Message, "expected auth message with username")
}

func TestStoreHandleSendNotificationFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-history.json")

	store, err := NewStore(path, 10)
	require.NoError(t, err, "new store")

	store.now = func() time.Time { return time.Date(2026, 3, 22, 10, 4, 0, 0, time.UTC) }
	require.NoError(
		t,
		store.Handle(context.Background(), &events.SendNotificationFailed{
			EventType:   events.TypeDeploySuccess,
			Destination: "telegram",
			Channel:     "ops",
			Error:       errors.New("request timeout"),
		}),
		"save send notification failed event",
	)

	items := store.List()
	require.Len(t, items, 1, "expected single event")
	assert.Equal(t, events.Type(events.TypeSendNotificationFailed), items[0].Type,
		"expected send notification failed type")
	assert.Equal(t, "telegram", items[0].Details["destination"], "expected destination")
	assert.Equal(t, "ops", items[0].Details["channel"], "expected channel")
	assert.Equal(t, "request timeout", items[0].Details["error"], "expected error text")
	assert.Equal(t, string(events.TypeDeploySuccess), items[0].Details["event_type"], "expected source event type")
	assert.Equal(
		t,
		"Send notification failed to telegram channel ops for deploySuccess",
		items[0].Message,
		"expected message",
	)
}
