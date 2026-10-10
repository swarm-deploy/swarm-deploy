package history

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLiteQueryMatchesLegacyPagination(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)
	now := time.Date(2025, 1, 1, 0, 0, 0, 123456789, time.UTC)
	entries := make([]Entry, 0, 30)
	for i := 0; i < 30; i++ {
		typ := events.TypeDeploySuccess
		if i%3 == 0 {
			typ = events.TypeNodeJoined
		}
		entries = append(entries, Entry{ID: fmt.Sprintf("id-%02d", i), Type: typ, Severity: []events.Severity{events.SeverityInfo, events.SeverityWarn, events.SeverityError}[i%3], Category: typ.Category(), CreatedAt: now.Add(time.Duration(i/3) * time.Nanosecond)})
	}
	require.NoError(t, db.WithinTransaction(ctx, func(ctx context.Context) error { return repo.Import(ctx, entries) }))
	legacy := &Store{entries: entries}
	for _, limit := range []int{0, 1, 4, 100} {
		options := QueryOptions{Limit: limit, Types: []events.TypeName{events.TypeNodeJoined.Name()}, Since: &now}
		expected, queryErr := legacy.ReadRecent(ctx, options)
		require.NoError(t, queryErr)
		actual, queryErr := repo.ReadRecent(ctx, options)
		require.NoError(t, queryErr)
		assert.Equal(t, expected, actual)
	}
	for _, order := range []SortOrder{SortTimeAsc, SortTimeDesc, SortSeverityAsc, SortSeverityDesc} {
		for _, filter := range []QueryOptions{{}, {Severities: []events.Severity{events.SeverityInfo, events.SeverityError}}, {Types: []events.TypeName{events.TypeNodeJoined.Name()}, Since: &now}, {Categories: []events.Category{events.TypeDeploySuccess.Category()}}} {
			t.Run(string(order)+fmt.Sprint(filter), func(t *testing.T) {
				filter.Sort = order
				filter.Limit = 4
				for {
					expected, queryErr := legacy.Query(filter)
					require.NoError(t, queryErr)
					actual, queryErr := repo.QueryPage(ctx, filter)
					require.NoError(t, queryErr)
					assert.Equal(t, expected, actual)
					if actual.NextCursor == "" {
						break
					}
					filter.Cursor = actual.NextCursor
				}
			})
		}
	}
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

func TestLegacyImportPrecedesNewPublicationSequence(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo, err := NewSQLStore(db, 10)
	require.NoError(t, err)
	at := time.Now().UTC()
	legacy := []Entry{
		{ID: "legacy-1", Type: events.TypeNodeJoined, Severity: events.SeverityInfo, Category: events.CategorySwarm, CreatedAt: at},
		{ID: "legacy-2", Type: events.TypeNodeJoined, Severity: events.SeverityInfo, Category: events.CategorySwarm, CreatedAt: at},
	}
	require.NoError(t, db.WithinTransaction(ctx, func(ctx context.Context) error { return repo.Import(ctx, legacy) }))
	bus := outbox.New(db)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "history", repo))
	require.NoError(t, bus.Publish(ctx, &events.NodeJoined{NodeID: "current"}))
	processed, err := bus.ProcessNext(ctx)
	require.NoError(t, err)
	assert.True(t, processed)
	entries, err := repo.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "legacy-1", entries[0].ID)
	assert.Equal(t, "legacy-2", entries[1].ID)
	assert.NotEqual(t, "legacy-1", entries[2].ID)
	assert.NotEqual(t, "legacy-2", entries[2].ID)
}
