package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Thiht/transactor/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestartAndRollback(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	require.NoError(t, err)
	for _, tc := range []struct {
		id       string
		rollback bool
	}{{"committed", false}, {"rolled-back", true}} {
		t.Run(tc.id, func(t *testing.T) {
			sentinel := errors.New("rollback")
			err := db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := db.Exec(ctx, `INSERT INTO outbox_events(id,event_type,schema_version,occurred_at_ms,payload) VALUES (?,'nodeJoined',1,0,'{}')`, tc.id)
				if err != nil {
					return err
				}
				if tc.rollback {
					return sentinel
				}
				return nil
			})
			if tc.rollback {
				require.ErrorIs(t, err, sentinel)
			} else {
				require.NoError(t, err)
			}
		})
	}
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var id string
	require.NoError(t, db.QueryRow(t.Context(), "SELECT id FROM outbox_events").Scan(&id))
	assert.Equal(t, "committed", id)
	var count int
	require.NoError(t, db.QueryRow(
		t.Context(),
		"SELECT count(*) FROM goose_db_version WHERE version_id = 1 AND is_applied = 1",
	).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestFreshDatabaseHasAlertIndexedSchema(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	rows, err := db.Query(t.Context(), "PRAGMA table_info(alerts)")
	require.NoError(t, err)
	defer rows.Close()
	notNull := map[string]bool{}
	for rows.Next() {
		var cid, required, primaryKey int
		var name, columnType string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &required, &defaultValue, &primaryKey))
		notNull[name] = required == 1
	}
	require.NoError(t, rows.Err())
	assert.True(t, notNull["kind"])
	assert.True(t, notNull["resource_type"])
	assert.True(t, notNull["resource_id"])
}

func TestNestedTransactions(t *testing.T) {
	sentinel := errors.New("rollback requested")
	for _, tc := range []struct {
		name      string
		innerErr  error
		outerErr  error
		wantCount int
	}{
		{"commit", nil, nil, 2},
		{"inner rollback keeps outer", sentinel, nil, 1},
		{"outer rollback includes inner commit", nil, sentinel, 0},
		{"both roll back", sentinel, sentinel, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			_, err = db.Exec(t.Context(), "CREATE TABLE entries (id TEXT PRIMARY KEY)")
			require.NoError(t, err)
			err = db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				assert.True(t, stdlib.IsWithinTransaction(ctx))
				_, err := db.Exec(ctx, "INSERT INTO entries VALUES ('outer')")
				if err != nil {
					return err
				}
				innerErr := db.WithinTransaction(ctx, func(inner context.Context) error {
					assert.True(t, stdlib.IsWithinTransaction(inner))
					_, err := db.Exec(inner, "INSERT INTO entries VALUES ('inner')")
					if err != nil {
						return err
					}
					return tc.innerErr
				})
				require.ErrorIs(t, innerErr, tc.innerErr)
				return tc.outerErr
			})
			require.ErrorIs(t, err, tc.outerErr)
			var count int
			require.NoError(t, db.QueryRow(t.Context(), "SELECT count(*) FROM entries").Scan(&count))
			assert.Equal(t, tc.wantCount, count)
		})
	}
}

func TestConcurrentTransactions(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), "CREATE TABLE entries (id TEXT PRIMARY KEY)")
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			results <- db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := db.Exec(ctx, "INSERT INTO entries VALUES (?)", fmt.Sprint(i))
				return err
			})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, db.QueryRow(t.Context(), "SELECT count(*) FROM entries").Scan(&count))
	assert.Equal(t, 16, count)
}

func TestDatabaseSQLMethodsUseActiveTransaction(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), "CREATE TABLE method_entries (id TEXT PRIMARY KEY)")
	require.NoError(t, err)

	rollback := errors.New("rollback")
	err = db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		_, execErr := db.Exec(ctx, "INSERT INTO method_entries VALUES (?)", "transactional")
		if execErr != nil {
			return execErr
		}
		var id string
		if scanErr := db.QueryRow(ctx, "SELECT id FROM method_entries WHERE id=?", "transactional").Scan(&id); scanErr != nil {
			return scanErr
		}
		assert.Equal(t, "transactional", id)
		rows, queryErr := db.Query(ctx, "SELECT id FROM method_entries")
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&id))
		assert.Equal(t, "transactional", id)
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	var count int
	require.NoError(t, db.QueryRow(t.Context(), "SELECT count(*) FROM method_entries").Scan(&count))
	assert.Zero(t, count, "all SQL methods must use the rolled-back transaction")
}

func TestDatabaseSQLMethodsPropagateErrorsAndCancellation(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "query", run: func(ctx context.Context) error {
			rows, queryErr := db.Query(ctx, "SELECT 1")
			if rows != nil {
				_ = rows.Close()
			}
			return queryErr
		}},
		{name: "query row", run: func(ctx context.Context) error {
			var value int
			return db.QueryRow(ctx, "SELECT 1").Scan(&value)
		}},
		{name: "exec", run: func(ctx context.Context) error {
			_, execErr := db.Exec(ctx, "CREATE TABLE canceled (id INTEGER)")
			return execErr
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, tc.run(ctx), context.Canceled)
		})
	}

	_, err = db.Exec(t.Context(), "invalid SQL")
	require.Error(t, err)
	var value int
	require.Error(t, db.QueryRow(t.Context(), "SELECT missing FROM nowhere").Scan(&value))
}

func TestPragmasOnEveryPooledConnection(t *testing.T) {
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for range maxConnections {
		conn, err := db.db.Conn(t.Context())
		require.NoError(t, err)
		defer conn.Close()
		var foreignKeys, busyTimeout, synchronous int
		var journal string
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busyTimeout))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&synchronous))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal))
		assert.Equal(t, 1, foreignKeys)
		assert.Equal(t, 5000, busyTimeout)
		assert.Equal(t, 2, synchronous)
		assert.Equal(t, "wal", journal)
	}
}

func TestMigrationIsNotAppliedTwice(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	require.NoError(t, err)
	_, err = db.Exec(
		t.Context(),
		"INSERT INTO nodes(id, hostname, payload) VALUES ('node-1', 'node-1', '{}')",
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count int
	require.NoError(t, db.QueryRow(
		t.Context(),
		"SELECT count(*) FROM nodes WHERE id = 'node-1'",
	).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestOpenIgnoresLegacyJSONFiles(t *testing.T) {
	dir := t.TempDir()
	legacyFiles := []string{
		"controller.state.json", "event-history.json", "nodes.json", "alerts.state.json",
		"secrets.state.json", "recommendations.state.json", "services.json",
		filepath.Join("assistant", "chats", "index.json"),
	}
	for _, name := range legacyFiles {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{invalid legacy json"), 0o600))
	}

	db, err := Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, name := range legacyFiles {
		payload, readErr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, readErr)
		assert.Equal(t, "{invalid legacy json", string(payload), "legacy files must remain untouched")
	}
	var importedRows int
	require.NoError(t, db.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM deployments) +
			(SELECT count(*) FROM event_history) +
			(SELECT count(*) FROM alerts) +
			(SELECT count(*) FROM recommendations)
	`).Scan(&importedRows))
	assert.Zero(t, importedRows, "legacy files must not create SQLite records")
	var legacyTableCount int
	require.NoError(t, db.QueryRow(
		t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='legacy_imports'",
	).Scan(&legacyTableCount))
	assert.Zero(t, legacyTableCount)
}
