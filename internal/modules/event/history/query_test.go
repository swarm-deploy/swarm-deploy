package history

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func newQueryStore(t *testing.T, entries []Entry) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "history.json"), 100, fs.NewLocalFileSystem())
	require.NoError(t, err)
	store.entries = entries
	return store
}

func queryEntry(id string, minute int, severity events.Severity) Entry {
	return Entry{
		ID: id, CreatedAt: time.Date(2026, 10, 8, 12, minute, 0, 0, time.UTC),
		Type: events.TypeDeploySuccess, Category: events.CategorySync,
		Severity: severity, Message: id,
	}
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func TestQueryCursorIsStableWhenNewEventsArrive(t *testing.T) {
	store := newQueryStore(t, []Entry{
		queryEntry("a", 1, events.SeverityInfo),
		queryEntry("b", 2, events.SeverityInfo),
		queryEntry("c", 3, events.SeverityInfo),
		queryEntry("d", 4, events.SeverityInfo),
	})
	first, err := store.Query(QueryOptions{Limit: 2, Sort: SortTimeDesc})
	require.NoError(t, err)
	assert.Equal(t, []string{"d", "c"}, entryIDs(first.Entries))
	require.NotEmpty(t, first.NextCursor)

	store.entries = append(store.entries, queryEntry("e", 5, events.SeverityInfo))
	second, err := store.Query(QueryOptions{Limit: 2, Sort: SortTimeDesc, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, entryIDs(second.Entries))
	assert.Empty(t, second.NextCursor)

	// The cursor still works if the event that established the boundary
	// disappears due to capacity-based eviction.
	store.entries = []Entry{queryEntry("a", 1, events.SeverityInfo), queryEntry("b", 2, events.SeverityInfo)}
	second, err = store.Query(QueryOptions{Limit: 2, Sort: SortTimeDesc, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, entryIDs(second.Entries))
}

func TestQuerySortAndFilterBeforePagination(t *testing.T) {
	store := newQueryStore(t, []Entry{
		queryEntry("a", 1, events.SeverityInfo),
		queryEntry("b", 2, events.SeverityAlert),
		queryEntry("c", 3, events.SeverityWarn),
		queryEntry("d", 4, events.SeverityAlert),
		queryEntry("e", 5, events.SeverityInfo),
	})
	first, err := store.Query(QueryOptions{Limit: 2, Sort: SortSeverityDesc})
	require.NoError(t, err)
	assert.Equal(t, []string{"d", "b"}, entryIDs(first.Entries))
	require.NotEmpty(t, first.NextCursor)

	second, err := store.Query(QueryOptions{Limit: 2, Sort: SortSeverityDesc, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "e"}, entryIDs(second.Entries))

	third, err := store.Query(QueryOptions{Limit: 2, Sort: SortSeverityDesc, Cursor: second.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, entryIDs(third.Entries))
	assert.Empty(t, third.NextCursor)

	filtered, err := store.Query(QueryOptions{
		Limit: 2, Sort: SortTimeAsc,
		Severities: []events.Severity{events.SeverityInfo},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "e"}, entryIDs(filtered.Entries))
	assert.Empty(t, filtered.NextCursor)
}

func TestQueryCopiesOnlySelectedEntries(t *testing.T) {
	entry := queryEntry("a", 1, events.SeverityInfo)
	entry.Details = map[string]string{"stack": "original"}
	store := newQueryStore(t, []Entry{entry})
	page, err := store.Query(QueryOptions{Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	page.Entries[0].Details["stack"] = "modified"
	assert.Equal(t, "original", store.entries[0].Details["stack"])
}

func TestQueryRejectsInvalidCursors(t *testing.T) {
	store := newQueryStore(t, []Entry{queryEntry("a", 1, events.SeverityInfo)})
	first, err := store.Query(QueryOptions{Limit: 1, Sort: SortTimeDesc})
	require.NoError(t, err)
	assert.Empty(t, first.NextCursor)

	cursor := encodePageCursor(store.entries[0], SortTimeDesc)
	for _, options := range []QueryOptions{
		{Limit: 1, Cursor: "invalid", Sort: SortTimeDesc},
		{Limit: 1, Cursor: cursor, Sort: SortTimeAsc},
		{Limit: 1, Sort: "invalid"},
	} {
		_, err := store.Query(options)
		require.Error(t, err)
	}
	_, err = store.Query(QueryOptions{Limit: 1, Cursor: "invalid"})
	assert.ErrorIs(t, err, ErrInvalidCursor)
	_, err = store.Query(QueryOptions{Limit: 1, Sort: "invalid"})
	assert.True(t, errors.Is(err, ErrInvalidSort))
}
