package history

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLiteQueryPagination(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)
	now := time.Date(2025, 1, 1, 0, 0, 0, 123456789, time.UTC)
	entries := make([]Entry, 0, 30)
	for i := 0; i < 30; i++ {
		typ := events.TypeDeploySuccess
		if i%3 == 0 {
			typ = events.TypeNodeJoined
		}
		entries = append(entries, Entry{
			ID: fmt.Sprintf("id-%02d", i), Type: typ,
			Severity: []events.Severity{events.SeverityInfo, events.SeverityWarn, events.SeverityError}[i%3],
			Category: typ.Category(), CreatedAt: now.Add(time.Duration(i/3) * time.Nanosecond),
		})
	}
	require.NoError(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		for i, entry := range entries {
			if insertErr := repo.insert(ctx, entry, nil, int64(i+1)); insertErr != nil {
				return insertErr
			}
		}
		return nil
	}))

	recent, err := repo.ReadRecent(ctx, QueryOptions{
		Limit: 4, Types: []events.TypeName{events.TypeNodeJoined.Name()}, Since: &now,
	})
	require.NoError(t, err)
	require.Len(t, recent, 4)
	assert.Equal(t, []string{"id-18", "id-21", "id-24", "id-27"}, entryIDs(recent))

	filter := QueryOptions{Limit: 4, Sort: SortTimeDesc}
	first, err := repo.QueryPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, first.Entries, 4)
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, []string{"id-27", "id-28", "id-29", "id-24"}, entryIDs(first.Entries))

	filter.Cursor = first.NextCursor
	second, err := repo.QueryPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, second.Entries, 4)
	assert.NotEqual(t, entryIDs(first.Entries), entryIDs(second.Entries))
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestSQLiteProjectionSelectionIdempotenceAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo, err := NewSQLStore(db, 2)
	require.NoError(t, err)
	now := time.Now().UTC()
	e := events.Envelope{ID: "joined", OccurredAt: now, PublicationSequence: 1, Event: &events.NodeJoined{NodeID: "node"}}
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.Handle(ctx, e))
		return assert.AnError
	}), assert.AnError)
	entries, err := repo.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, entries)
	for _, envelope := range []events.Envelope{
		e, e,
		{ID: "failure", OccurredAt: now, Event: &events.DeployFailed{}},
		{ID: "disconnected", OccurredAt: now, Event: &events.NodeDisconnected{NodeID: "node"}},
	} {
		require.NoError(t, repo.Handle(ctx, envelope))
	}
	entries, err = repo.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "joined", entries[0].ID)
	for i, id := range []string{"next", "last"} {
		e.ID = id
		e.PublicationSequence = int64(i + 2)
		require.NoError(t, repo.Handle(ctx, e))
	}
	entries, err = repo.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "next", entries[0].ID)
}

func TestSQLiteRetentionUsesPublicationOrderAcrossDelayedDeliveries(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo, err := NewSQLStore(db, 1)
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := events.Envelope{
		ID: "newer", OccurredAt: at, PublicationSequence: 2,
		Event: &events.NodeJoined{NodeID: "newer"},
	}
	older := events.Envelope{
		ID: "older", OccurredAt: at, PublicationSequence: 1,
		Event: &events.NodeJoined{NodeID: "older"},
	}
	require.NoError(t, repo.Handle(ctx, newer))
	require.NoError(t, repo.Handle(ctx, older))
	require.NoError(t, repo.Handle(ctx, older), "retry must remain idempotent")
	entries, err := repo.ReadRecent(ctx, QueryOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "newer", entries[0].ID)

	page, err := repo.QueryPage(ctx, QueryOptions{Limit: 1, Sort: SortTimeDesc})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "newer", page.Entries[0].ID)
}
