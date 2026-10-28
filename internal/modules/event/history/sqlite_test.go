package history

import (
	"context"
	"errors"
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
	db, err := storage.OpenSQLite(ctx, t.TempDir())
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
			if insertErr := repo.insert(ctx, entry, int64(i+1)); insertErr != nil {
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

func TestSQLiteQueryFiltersAttributesBeforePagination(t *testing.T) {
	t.Parallel()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{ID: "api-worker-old", Type: events.TypeServiceMissed, Severity: events.SeverityWarn,
			Category: events.CategorySwarm, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "api", events.AttributeServiceName: "worker"}},
		{ID: "web-worker", Type: events.TypeServiceMissed, Severity: events.SeverityWarn,
			Category: events.CategorySwarm, CreatedAt: at.Add(time.Second),
			Details: map[string]string{events.AttributeStackName: "web", events.AttributeServiceName: "worker"}},
		{ID: "api-database", Type: events.TypeServiceMissed, Severity: events.SeverityWarn,
			Category: events.CategorySwarm, CreatedAt: at.Add(2 * time.Second),
			Details: map[string]string{events.AttributeStackName: "api", events.AttributeServiceName: "database"}},
		{ID: "no-service", Type: events.TypeDeploySuccess, Severity: events.SeverityInfo,
			Category: events.CategorySync, CreatedAt: at.Add(3 * time.Second),
			Details: map[string]string{events.AttributeStackName: "api"}},
		{ID: "api-worker-new", Type: events.TypeServiceRestarted, Severity: events.SeverityWarn,
			Category: events.CategorySwarm, CreatedAt: at.Add(4 * time.Second),
			Details: map[string]string{events.AttributeStackName: "api", events.AttributeServiceName: "worker"}},
	}
	for index, entry := range entries {
		require.NoError(t, repo.insert(t.Context(), entry, int64(index+1)))
	}

	options := QueryOptions{
		StackNames: []string{"api", "jobs"}, ServiceNames: []string{"worker"},
		Categories: []events.Category{events.CategorySwarm}, Limit: 1, Sort: SortTimeDesc,
	}
	first, err := repo.QueryPage(t.Context(), options)
	require.NoError(t, err)
	require.Len(t, first.Entries, 1)
	assert.Equal(t, "api-worker-new", first.Entries[0].ID)
	require.NotEmpty(t, first.NextCursor)

	options.Cursor = first.NextCursor
	second, err := repo.QueryPage(t.Context(), options)
	require.NoError(t, err)
	require.Len(t, second.Entries, 1)
	assert.Equal(t, "api-worker-old", second.Entries[0].ID)
	assert.Empty(t, second.NextCursor)

	missing, err := repo.QueryPage(t.Context(), QueryOptions{
		ServiceNames: []string{"worker"}, Categories: []events.Category{events.CategorySync},
		Limit: 10, Sort: SortTimeDesc,
	})
	require.NoError(t, err)
	assert.Empty(t, missing.Entries, "events without service_name must not match")
}

func TestSQLiteFilterOptionsUsesRetainedHistory(t *testing.T) {
	t.Parallel()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	entries := []Entry{
		{ID: "deleted-service", Type: events.TypeServiceMissed, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "removed", events.AttributeServiceName: "worker"}},
		{ID: "duplicate", Type: events.TypeServiceRestarted, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "removed", events.AttributeServiceName: "worker"}},
		{ID: "same-name-other-stack", Type: events.TypeServiceMissed, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "active", events.AttributeServiceName: "worker"}},
		{ID: "stack-only", Type: events.TypeDeploySuccess, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "archive"}},
		{ID: "empty", Type: events.TypeDeploySuccess, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: " ", events.AttributeServiceName: ""}},
		{ID: "service-only", Type: events.TypeServiceMissed, CreatedAt: at,
			Details: map[string]string{events.AttributeServiceName: "orphan"}},
	}
	for index, entry := range entries {
		entry.Severity = entry.Type.Severity()
		entry.Category = entry.Type.Category()
		require.NoError(t, repo.insert(t.Context(), entry, int64(index+1)))
	}

	options, err := repo.FilterOptions(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"active", "archive", "removed"}, options.Stacks)
	assert.Equal(t, []FilterService{
		{StackName: "active", ServiceName: "worker"},
		{StackName: "removed", ServiceName: "worker"},
	}, options.Services)
}

func TestSQLiteQueriesExcludeLegacySyncFailedHistory(t *testing.T) {
	t.Parallel()
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)
	at := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	valid := []Entry{
		{ID: "older", Type: events.TypeDeploySuccess, Severity: events.SeverityInfo,
			Category: events.CategorySync, CreatedAt: at,
			Details: map[string]string{events.AttributeStackName: "api"}},
		{ID: "newer", Type: events.TypeServiceMissed, Severity: events.SeverityWarn,
			Category: events.CategorySwarm, CreatedAt: at.Add(2 * time.Second),
			Details: map[string]string{events.AttributeStackName: "web", events.AttributeServiceName: "worker"}},
	}
	require.NoError(t, repo.insert(t.Context(), valid[0], 1))
	_, err = db.Exec(t.Context(), `INSERT INTO event_history
		(publication_sequence,id,event_type,severity,severity_rank,category,created_at,message,details)
		VALUES (?,?,?,?,?,?,?,?,?)`, 2, "legacy", legacySyncFailedEventType, events.SeverityError,
		severityRank(events.SeverityError), events.CategorySync, at.Add(time.Second),
		"legacy sync failure", `{"stack_name":"legacy","service_name":"legacy-service"}`)
	require.NoError(t, err)
	require.NoError(t, repo.insert(t.Context(), valid[1], 3))

	entries, err := repo.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"older", "newer"}, entryIDs(entries))

	recent, err := repo.ReadRecent(t.Context(), QueryOptions{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"newer"}, entryIDs(recent))

	options := QueryOptions{Limit: 1, Sort: SortTimeDesc}
	first, err := repo.QueryPage(t.Context(), options)
	require.NoError(t, err)
	assert.Equal(t, []string{"newer"}, entryIDs(first.Entries))
	require.NotEmpty(t, first.NextCursor)
	options.Cursor = first.NextCursor
	second, err := repo.QueryPage(t.Context(), options)
	require.NoError(t, err)
	assert.Equal(t, []string{"older"}, entryIDs(second.Entries))
	assert.Empty(t, second.NextCursor)

	legacyOnly, err := repo.QueryPage(t.Context(), QueryOptions{
		Limit: 10, Sort: SortTimeDesc, Types: []events.TypeName{events.TypeName(legacySyncFailedEventType)},
	})
	require.NoError(t, err)
	assert.Empty(t, legacyOnly.Entries)

	filterOptions, err := repo.FilterOptions(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"api", "web"}, filterOptions.Stacks)
	assert.Equal(t, []FilterService{{StackName: "web", ServiceName: "worker"}}, filterOptions.Services)

	var legacyCount int
	require.NoError(t, db.Get(t.Context(), &legacyCount,
		`SELECT count(*) FROM event_history WHERE event_type=?`, legacySyncFailedEventType))
	assert.Equal(t, 1, legacyCount, "legacy rows must remain unchanged")
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
	db, err := storage.OpenSQLite(ctx, t.TempDir())
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
		{ID: "configuration", OccurredAt: now, Event: &events.ConfigurationChanged{}},
		{ID: "removed-desired", OccurredAt: now, Event: &events.ServiceRemovedFromDesired{
			StackName: "app", ServiceName: "worker", Commit: "sha",
		}},
		{ID: "failure", OccurredAt: now, PublicationSequence: 2, Event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app", Commit: "sha"},
			Error:       assert.AnError, ErrorCode: "apply_failed", Stage: "apply",
		}},
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
		e.PublicationSequence = int64(i + 3)
		require.NoError(t, repo.Handle(ctx, e))
	}
	entries, err = repo.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "next", entries[0].ID)
}

func TestDeploymentFailuresAreExcludedFromEventHistory(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 10)
	require.NoError(t, err)
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	failures := []struct {
		id    string
		event events.Event
	}{
		{id: "deploy", event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{DeploymentID: "deployment-1", StackName: "app", Commit: "sha"},
			Error:       errors.New("token=private apply rejected"), ErrorCode: "apply_failed", Stage: "apply",
		}},
		{id: "prepare", event: &events.DeployPreparationFailed{
			StackName: "app", Commit: "sha", Error: errors.New("token=private invalid compose"),
			ErrorCode: "preparation_failed", Stage: "validation",
		}},
		{id: "interrupted", event: &events.DeployInterrupted{
			DeploymentID: "deployment-2", StackName: "app", Commit: "sha",
			Error: errors.New("token=private interrupted"), Reason: "process_interrupted", Stage: "apply",
		}},
	}
	for index, failure := range failures {
		envelope := events.Envelope{
			ID: failure.id, OccurredAt: at.Add(time.Duration(index) * time.Second),
			PublicationSequence: int64(index + 1), Event: failure.event,
		}
		require.NoError(t, repo.Handle(t.Context(), envelope))
		require.NoError(t, repo.Handle(t.Context(), envelope), "duplicate delivery must not create another history row")
	}

	entries, err := repo.Read(t.Context())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestSQLiteRetentionUsesPublicationOrderAcrossDelayedDeliveries(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
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

func TestSQLiteStoresNormalizedHistoryAndTargetsIDConflict(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 10)
	require.NoError(t, err)
	at := time.Date(2026, 4, 5, 6, 7, 8, 123456789, time.UTC)
	entry := Entry{
		ID: "event", Type: events.TypeNodeJoined, Severity: events.SeverityInfo,
		Category: events.TypeNodeJoined.Category(), CreatedAt: at,
		Message: "joined", Details: map[string]string{"node_id": "node-1"},
	}
	require.NoError(t, repo.insert(t.Context(), entry, 11))
	require.NoError(t, repo.insert(t.Context(), entry, 12), "same ID must be idempotent")

	var stored struct {
		CreatedType string `db:"created_type"`
		Details     string `db:"details"`
	}
	require.NoError(t, db.Get(t.Context(), &stored,
		`SELECT typeof(created_at) AS created_type,details FROM event_history WHERE id='event'`))
	assert.Equal(t, "integer", stored.CreatedType)
	assert.JSONEq(t, `{"node_id":"node-1"}`, stored.Details)

	var removedColumns int
	require.NoError(t, db.Get(t.Context(), &removedColumns,
		`SELECT count(*) FROM pragma_table_info('event_history')
		 WHERE name IN ('sequence','source_event_id','payload')`))
	assert.Zero(t, removedColumns)

	conflicting := entry
	conflicting.ID = "different-event"
	require.Error(t, repo.insert(t.Context(), conflicting, 11),
		"publication sequence conflicts must not be hidden by idempotency")
}

func TestSQLiteQueryPageUsesHistoryIndex(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var plan []struct {
		ID      int    `db:"id"`
		Parent  int    `db:"parent"`
		NotUsed int    `db:"notused"`
		Detail  string `db:"detail"`
	}
	require.NoError(t, db.Select(t.Context(), &plan,
		`EXPLAIN QUERY PLAN SELECT id,event_type,severity,category,created_at,message,details
		 FROM event_history WHERE event_type=? ORDER BY created_at DESC,id LIMIT 20`,
		events.TypeNodeJoined.Name()))
	require.NotEmpty(t, plan)
	assert.Contains(t, plan[0].Detail, "idx_history_type_time")
}

func TestServicePrunedAppearsInEventHistory(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo, err := NewSQLStore(db, 100)
	require.NoError(t, err)

	at := time.Date(2026, time.October, 22, 12, 0, 0, 0, time.UTC)
	event := &events.ServicePruned{StackName: "app", ServiceName: "old", Commit: "revision"}
	require.NoError(t, repo.Handle(ctx, events.Envelope{
		ID: "pruned-1", OccurredAt: at, PublicationSequence: 1, Event: event,
	}))

	page, err := repo.QueryPage(ctx, QueryOptions{
		Types:      []events.TypeName{events.TypeNameServicePruned},
		StackNames: []string{"app"}, ServiceNames: []string{"old"},
		Limit: 10, Sort: SortTimeDesc,
	})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "pruned-1", page.Entries[0].ID)
	assert.Equal(t, events.TypeServicePruned, page.Entries[0].Type)
	assert.Equal(t, "Service app/old pruned", page.Entries[0].Message)
	assert.Equal(t, events.SeverityInfo, page.Entries[0].Severity)
	assert.Equal(t, events.CategorySync, page.Entries[0].Category)
	assert.Equal(t, map[string]string{
		events.AttributeStackName:   "app",
		events.AttributeServiceName: "old",
		events.AttributeCommit:      "revision",
	}, page.Entries[0].Details)
}
