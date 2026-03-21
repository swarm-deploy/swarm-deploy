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
	assert.Equal(t, "boom", items[1].Error, "expected error text")

	reloaded, err := NewStore(path, 2)
	require.NoError(t, err, "reload store")
	reloadedItems := reloaded.List()
	require.Len(t, reloadedItems, 2, "expected persisted rotated history size")
	assert.Equal(t, items, reloadedItems, "expected persisted entries")
}
