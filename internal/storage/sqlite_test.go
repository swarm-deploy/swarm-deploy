package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	txsqlx "github.com/Thiht/transactor/sqlx"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlitemigrations "github.com/swarm-deploy/swarm-deploy/migrations/sqlite"
)

func openAtMigration1(t *testing.T) (*SQLXDatabase, *goose.Provider) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "swarm-deploy.sqlite")
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(FULL)&_txlock=immediate&_time_integer_format=unix_nano&_inttotime=1"
	driver, err := sqlx.Open("sqlite", u.String())
	require.NoError(t, err)
	driver.SetMaxOpenConns(maxConnections)
	driver.SetMaxIdleConns(maxConnections)
	transactor, get := txsqlx.NewTransactor(driver, txsqlx.NestedTransactionsSavepoints)
	db := &SQLXDatabase{db: driver, get: get, transactor: transactor}
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	provider, err := goose.NewProvider(goose.DialectSQLite3, driver.DB, sqlitemigrations.Files)
	require.NoError(t, err)
	_, err = provider.UpTo(t.Context(), 1)
	require.NoError(t, err)
	return db, provider
}

func TestDeploymentAttemptMigrationPreservesPopulatedV1Database(t *testing.T) {
	db, provider := openAtMigration1(t)
	finished := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	_, err := db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		"legacy-deployment", "app", "sha", "failed", "apply_failed", "apply",
		finished.Add(-time.Minute), finished, `{"digest":"digest"}`,
		`{"error_diagnostic":"old failure","apply_status":"failed","verification_status":"skipped","cleanup_status":"skipped","actual_state_status":"unknown","comparison_basis":"none","comparison_status":"unknown","summary":{},"resources":{},"diff_available":true}`,
		`[]`)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(),
		"INSERT INTO desired_snapshots(stack,deployment_id) VALUES (?,?)", "app", "legacy-deployment")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `INSERT INTO outbox_events
		(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
		"legacy-event", "DeployFailed", 1, finished, `{}`)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `INSERT INTO alerts
		(id,fingerprint,kind,resource_type,resource_id,deployment_id,status,opened_at,updated_at,payload)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"legacy-alert", "legacy-fingerprint", "deployment", "stack", "app", "legacy-deployment",
		"open", finished, finished, `{}`)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	var columnNames []string
	require.NoError(t, db.Select(t.Context(), &columnNames,
		"SELECT name FROM pragma_table_info('deployments') ORDER BY cid"))
	assert.Equal(t, []string{
		"id", "stack", "commit", "status", "error_code", "phase", "started_at", "finished_at",
		"desired_payload", "details_payload", "changes_payload",
	}, columnNames)
	var attempts int
	require.NoError(t, db.Get(t.Context(), &attempts, "SELECT count(*) FROM deployment_attempts"))
	assert.Zero(t, attempts, "historical executions are intentionally not backfilled")
	var status, diagnostic string
	require.NoError(t, db.Get(t.Context(), &status,
		"SELECT status FROM deployments WHERE id='legacy-deployment'"))
	require.NoError(t, db.Get(t.Context(), &diagnostic,
		`SELECT json_extract(details_payload,'$.error_diagnostic') FROM deployments WHERE id='legacy-deployment'`))
	assert.Equal(t, "failed", status)
	assert.Equal(t, "old failure", diagnostic)
	var baseline string
	require.NoError(t, db.Get(t.Context(), &baseline,
		"SELECT deployment_id FROM desired_snapshots WHERE stack='app'"))
	assert.Equal(t, "legacy-deployment", baseline)
	var preserved int
	require.NoError(t, db.Get(t.Context(), &preserved,
		`SELECT (SELECT count(*) FROM outbox_events WHERE id='legacy-event') +
		        (SELECT count(*) FROM alerts WHERE id='legacy-alert' AND deployment_id='legacy-deployment')`))
	assert.Equal(t, 2, preserved)
}

func TestOutboxTraceparentMigrationPreservesExistingEvents(t *testing.T) {
	db, provider := openAtMigration1(t)
	_, err := provider.UpTo(t.Context(), 2)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `INSERT INTO outbox_events
		(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
		"legacy-event", "NodeJoined", 1, time.Now().UTC(), `{}`)
	require.NoError(t, err)

	_, err = provider.Up(t.Context())
	require.NoError(t, err)
	var traceparent sql.NullString
	require.NoError(t, db.Get(t.Context(), &traceparent,
		"SELECT traceparent FROM outbox_events WHERE id='legacy-event'"))
	assert.False(t, traceparent.Valid)

	_, err = db.Exec(t.Context(), `INSERT INTO outbox_events
		(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
		"old-writer-event", "NodeJoined", 1, time.Now().UTC(), `{}`)
	require.NoError(t, err, "the nullable column must remain compatible with old writers")
}

func TestInitJobMigrationPreservesPopulatedV3Database(t *testing.T) {
	db, provider := openAtMigration1(t)
	_, err := provider.UpTo(t.Context(), 3)
	require.NoError(t, err)
	started := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,'{}','{}','[]')`,
		"deployment", "app", "commit", "running", "", "apply", started)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `INSERT INTO deployment_attempts
		(id,deployment_id,number,status,error_code,phase,started_at,finished_at,details_payload)
		VALUES (?,?,?,?,?,?,?,NULL,'{}')`,
		"attempt", "deployment", 1, "running", "", "apply", started)
	require.NoError(t, err)
	_, err = provider.Up(t.Context())
	require.NoError(t, err)

	var attempts, executions int
	require.NoError(t, db.Get(t.Context(), &attempts,
		"SELECT count(*) FROM deployment_attempts WHERE id='attempt' AND deployment_id='deployment'"))
	require.NoError(t, db.Get(t.Context(), &executions, "SELECT count(*) FROM deployment_init_jobs"))
	assert.Equal(t, 1, attempts)
	assert.Zero(t, executions, "historical Attempts must remain readable without synthetic Init Job executions")
	_, err = db.Exec(t.Context(), "UPDATE deployment_attempts SET error_code='' WHERE id='attempt'")
	require.NoError(t, err, "the additive table must remain compatible with old writers")
}

func TestDeploymentAttemptMigrationAllowsOldBinaryWrites(t *testing.T) {
	db, provider := openAtMigration1(t)
	_, err := provider.Up(t.Context())
	require.NoError(t, err)

	started := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	details := `{"apply_status":"running","verification_status":"pending","cleanup_status":"pending","actual_state_status":"unknown","comparison_basis":"none","comparison_status":"unknown","summary":{},"resources":{},"diff_available":true}`
	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,?,?,?)`,
		"old-writer-1", "app", "first", "running", "", "apply", started,
		`{"digest":"digest-1"}`, details, `[]`)
	require.NoError(t, err)

	finished := started.Add(time.Minute)
	failedDetails := `{"error_diagnostic":"old binary failure","apply_status":"failed","verification_status":"skipped","cleanup_status":"skipped","actual_state_status":"unknown","comparison_basis":"none","comparison_status":"unknown","summary":{},"resources":{},"diff_available":true}`
	_, err = db.Exec(t.Context(), `UPDATE deployments
		SET status='failed',error_code='apply_failed',phase='apply',finished_at=?,details_payload=?
		WHERE id='old-writer-1'`, finished, failedDetails)
	require.NoError(t, err)

	var deployment struct {
		Status     string `db:"status"`
		Diagnostic string `db:"diagnostic"`
	}
	require.NoError(t, db.Get(t.Context(), &deployment, `SELECT status,
		json_extract(details_payload,'$.error_diagnostic') AS diagnostic
		FROM deployments WHERE id='old-writer-1'`))
	assert.Equal(t, "failed", deployment.Status)
	assert.Equal(t, "old binary failure", deployment.Diagnostic)

	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,?,?,?)`,
		"old-writer-2", "app", "second", "running", "", "apply", finished.Add(time.Minute),
		`{"digest":"digest-2"}`, details, `[]`)
	require.NoError(t, err)
	var attempts int
	require.NoError(t, db.Get(t.Context(), &attempts,
		"SELECT count(*) FROM deployment_attempts WHERE deployment_id LIKE 'old-writer-%'"))
	assert.Zero(t, attempts)

	_, err = db.Exec(t.Context(), "UPDATE deployments SET status='superseded' WHERE id='old-writer-1'")
	require.Error(t, err, "the original deployment status constraint must remain unchanged")
}

func TestDeploymentAttemptMigrationIsForwardOnly(t *testing.T) {
	db, provider := openAtMigration1(t)
	_, err := provider.Up(t.Context())
	require.NoError(t, err)
	_, err = provider.Down(t.Context())
	require.NoError(t, err, "the Init Job table migration is reversible")
	_, err = provider.Down(t.Context())
	require.NoError(t, err, "the traceparent column migration is reversible")
	_, err = provider.Down(t.Context())
	require.Error(t, err, "the deployment attempt migration remains forward-only")

	version, err := provider.GetDBVersion(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 2, version)
	var tableCount int
	require.NoError(t, db.Get(t.Context(), &tableCount,
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='deployment_attempts'"))
	assert.Equal(t, 1, tableCount)
}

func TestTimestampRoundTrip(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), `CREATE TABLE timestamp_round_trip (
		id TEXT NOT NULL PRIMARY KEY,
		required_at TIMESTAMP NOT NULL CHECK (typeof(required_at) = 'integer'),
		optional_at TIMESTAMP CHECK (optional_at IS NULL OR typeof(optional_at) = 'integer')
	)`)
	require.NoError(t, err)

	source := time.Date(2026, 4, 5, 6, 7, 8, 123456789, time.FixedZone("test", 3*60*60))
	valid := sql.NullTime{Time: source, Valid: true}
	_, err = db.Exec(t.Context(),
		"INSERT INTO timestamp_round_trip(id,required_at,optional_at) VALUES (?,?,?)",
		"valid", source, valid,
	)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(),
		"INSERT INTO timestamp_round_trip(id,required_at,optional_at) VALUES (?,?,?)",
		"null", source.Add(time.Nanosecond), sql.NullTime{},
	)
	require.NoError(t, err)

	var row struct {
		Required time.Time    `db:"required_at"`
		Optional sql.NullTime `db:"optional_at"`
	}
	require.NoError(t, db.Get(t.Context(), &row,
		"SELECT required_at,optional_at FROM timestamp_round_trip WHERE id='valid'"))
	assert.Equal(t, source.UTC(), row.Required)
	assert.Equal(t, time.UTC, row.Required.Location())
	require.True(t, row.Optional.Valid)
	assert.Equal(t, source.UTC(), row.Optional.Time)
	assert.Equal(t, source.Nanosecond(), row.Required.Nanosecond())

	require.NoError(t, db.Get(t.Context(), &row,
		"SELECT required_at,optional_at FROM timestamp_round_trip WHERE id='null'"))
	assert.False(t, row.Optional.Valid)

	var storageTypes struct {
		Required string         `db:"required_type"`
		Optional sql.NullString `db:"optional_type"`
	}
	require.NoError(t, db.Get(t.Context(), &storageTypes,
		`SELECT typeof(required_at) AS required_type, typeof(optional_at) AS optional_type
		 FROM timestamp_round_trip WHERE id='valid'`))
	assert.Equal(t, "integer", storageTypes.Required)
	require.True(t, storageTypes.Optional.Valid)
	assert.Equal(t, "integer", storageTypes.Optional.String)

	var ids []string
	require.NoError(t, db.Select(t.Context(), &ids,
		"SELECT id FROM timestamp_round_trip ORDER BY required_at ASC"))
	assert.Equal(t, []string{"valid", "null"}, ids)
	var newer int
	require.NoError(t, db.Get(t.Context(), &newer,
		"SELECT count(*) FROM timestamp_round_trip WHERE required_at > ?", source))
	assert.Equal(t, 1, newer)

	txTime := source.Add(2 * time.Nanosecond)
	require.NoError(t, db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if _, execErr := db.Exec(ctx,
			"INSERT INTO timestamp_round_trip(id,required_at,optional_at) VALUES (?,?,NULL)",
			"transactional", txTime,
		); execErr != nil {
			return execErr
		}
		var got time.Time
		if getErr := db.Get(ctx, &got,
			"SELECT required_at FROM timestamp_round_trip WHERE id='transactional'",
		); getErr != nil {
			return getErr
		}
		assert.Equal(t, txTime.UTC(), got)
		return nil
	}))
}

func TestFreshDatabaseEnforcesForeignKeysAndChecks(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now := time.Date(2026, 4, 5, 6, 7, 8, 9, time.UTC)

	_, err = db.Exec(t.Context(), `INSERT INTO assistant_chats
		(id,title,created_at,updated_at,input_tokens,output_tokens,total_tokens)
		VALUES (?,?,?,?,NULL,NULL,NULL)`, "chat", "Chat", now, now)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(),
		"INSERT INTO assistant_turns(chat_id,sequence,payload) VALUES (?,?,?)", "chat", 0, `{}`)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "UPDATE assistant_chats SET input_tokens=0 WHERE id='chat'")
	require.Error(t, err, "usage counters must be either all NULL or all present")
	_, err = db.Exec(t.Context(), "DELETE FROM assistant_chats WHERE id='chat'")
	require.NoError(t, err)
	var turns int
	require.NoError(t, db.Get(t.Context(), &turns, "SELECT count(*) FROM assistant_turns"))
	assert.Zero(t, turns, "chat deletion must cascade to turns")

	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,'{}','{}','[]')`,
		"invalid-status", "app", "commit", "unknown", "", "apply", now)
	require.Error(t, err)
	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,'{}','{}','[]')`,
		"invalid-terminal-time", "app", "commit", "succeeded", "", "completed", now)
	require.Error(t, err)
	_, err = db.Exec(t.Context(),
		"INSERT INTO desired_snapshots(stack,deployment_id) VALUES (?,?)", "app", "missing")
	require.Error(t, err, "snapshot must reference an existing deployment")
	_, err = db.Exec(t.Context(), `INSERT INTO outbox_events
		(id,event_type,schema_version,occurred_at,payload) VALUES (?,?,?,?,?)`,
		"text-time", "NodeJoined", 1, "2026-04-05T06:07:08Z", `{}`)
	require.Error(t, err, "SQL timestamps must be stored as INTEGER")

	for _, table := range []string{"notification_messages", "alert_failure_streaks"} {
		var count int
		require.NoError(t, db.Get(t.Context(), &count,
			"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table))
		assert.Equal(t, 1, count, "%s must be part of the initial schema", table)
	}
	var appliedVersions int
	require.NoError(t, db.Get(t.Context(), &appliedVersions,
		"SELECT count(*) FROM goose_db_version WHERE version_id > 0 AND is_applied = 1"))
	assert.Equal(t, 4, appliedVersions)
}

func TestRestartAndRollback(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	for _, tc := range []struct {
		id       string
		rollback bool
	}{{"committed", false}, {"rolled-back", true}} {
		t.Run(tc.id, func(t *testing.T) {
			sentinel := errors.New("rollback")
			err := db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := db.Exec(ctx, `INSERT INTO outbox_events(id,event_type,schema_version,occurred_at,payload) VALUES (?,'NodeJoined',1,0,'{}')`, tc.id)
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
	db, err = OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var id string
	require.NoError(t, db.Get(t.Context(), &id, "SELECT id FROM outbox_events"))
	assert.Equal(t, "committed", id)
	var count int
	require.NoError(t, db.Get(
		t.Context(),
		&count,
		"SELECT count(*) FROM goose_db_version WHERE version_id = 1 AND is_applied = 1",
	))
	assert.Equal(t, 1, count)
}

func TestFreshDatabaseHasAlertIndexedSchema(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var columns []struct {
		CID          int            `db:"cid"`
		Name         string         `db:"name"`
		Type         string         `db:"type"`
		Required     int            `db:"notnull"`
		DefaultValue sql.NullString `db:"dflt_value"`
		PrimaryKey   int            `db:"pk"`
	}
	require.NoError(t, db.Select(t.Context(), &columns, "PRAGMA table_info(alerts)"))
	notNull := map[string]bool{}
	for _, column := range columns {
		notNull[column.Name] = column.Required == 1
	}
	assert.True(t, notNull["kind"])
	assert.True(t, notNull["resource_type"])
	assert.True(t, notNull["resource_id"])
}

func TestNestedTransactions(t *testing.T) {
	sentinel := errors.New("rollback requested")
	for _, tc := range []struct {
		name        string
		innerErr    error
		outerErr    error
		returnInner bool
		wantCount   int
	}{
		{name: "commit", wantCount: 2},
		{name: "inner error joins outer when not returned", innerErr: sentinel, wantCount: 2},
		{name: "returned inner error rolls back outer", innerErr: sentinel, returnInner: true, wantCount: 0},
		{name: "outer rollback includes inner commit", outerErr: sentinel, wantCount: 0},
		{name: "both roll back", innerErr: sentinel, outerErr: sentinel, wantCount: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := OpenSQLite(t.Context(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			_, err = db.Exec(t.Context(), "CREATE TABLE entries (id TEXT PRIMARY KEY)")
			require.NoError(t, err)
			err = db.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := db.Exec(ctx, "INSERT INTO entries VALUES ('outer')")
				if err != nil {
					return err
				}
				innerErr := db.WithinTransaction(ctx, func(inner context.Context) error {
					_, err := db.Exec(inner, "INSERT INTO entries VALUES ('inner')")
					if err != nil {
						return err
					}
					return tc.innerErr
				})
				require.ErrorIs(t, innerErr, tc.innerErr)
				if tc.returnInner {
					return innerErr
				}
				return tc.outerErr
			})
			if tc.returnInner {
				require.ErrorIs(t, err, tc.innerErr)
			} else {
				require.ErrorIs(t, err, tc.outerErr)
			}
			var count int
			require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM entries"))
			assert.Equal(t, tc.wantCount, count)
		})
	}
}

func TestConcurrentTransactions(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
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
	require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM entries"))
	assert.Equal(t, 16, count)
}

func TestDatabaseSQLMethodsUseActiveTransaction(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
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
		if scanErr := db.Get(ctx, &id, "SELECT id FROM method_entries WHERE id=?", "transactional"); scanErr != nil {
			return scanErr
		}
		assert.Equal(t, "transactional", id)
		var ids []string
		if queryErr := db.Select(ctx, &ids, "SELECT id FROM method_entries"); queryErr != nil {
			return queryErr
		}
		require.Equal(t, []string{"transactional"}, ids)
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	var count int
	require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM method_entries"))
	assert.Zero(t, count, "all SQL methods must use the rolled-back transaction")
}

func TestDatabaseMethodsPropagateErrorsAndCancellation(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, tc := range []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "select", run: func(ctx context.Context) error {
			var values []int
			return db.Select(ctx, &values, "SELECT 1")
		}},
		{name: "get", run: func(ctx context.Context) error {
			var value int
			return db.Get(ctx, &value, "SELECT 1")
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
	require.Error(t, db.Get(t.Context(), &value, "SELECT missing FROM nowhere"))
}

func TestGetAndSelect(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(t.Context(), "CREATE TABLE widgets (id TEXT PRIMARY KEY, label TEXT NOT NULL)")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "INSERT INTO widgets VALUES ('a','Alpha'),('b','Beta')")
	require.NoError(t, err)

	type widget struct {
		ID    string `db:"id"`
		Label string `db:"label"`
	}

	t.Run("get scalar", func(t *testing.T) {
		var count int
		require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM widgets"))
		assert.Equal(t, 2, count)
	})

	t.Run("get struct", func(t *testing.T) {
		var w widget
		require.NoError(t, db.Get(t.Context(), &w, "SELECT id,label FROM widgets WHERE id=?", "a"))
		assert.Equal(t, widget{ID: "a", Label: "Alpha"}, w)
	})

	t.Run("get no rows", func(t *testing.T) {
		var w widget
		err := db.Get(t.Context(), &w, "SELECT id,label FROM widgets WHERE id=?", "missing")
		require.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("select structs", func(t *testing.T) {
		var widgets []widget
		require.NoError(t, db.Select(t.Context(), &widgets, "SELECT id,label FROM widgets ORDER BY id"))
		assert.Equal(t, []widget{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}, widgets)
	})

	t.Run("select scalars", func(t *testing.T) {
		var labels []string
		require.NoError(t, db.Select(t.Context(), &labels, "SELECT label FROM widgets ORDER BY id"))
		assert.Equal(t, []string{"Alpha", "Beta"}, labels)
	})

	t.Run("use active transaction and roll back", func(t *testing.T) {
		rollback := errors.New("rollback")
		err := db.WithinTransaction(t.Context(), func(ctx context.Context) error {
			_, execErr := db.Exec(ctx, "INSERT INTO widgets VALUES ('c','Gamma')")
			if execErr != nil {
				return execErr
			}
			var w widget
			if getErr := db.Get(ctx, &w, "SELECT id,label FROM widgets WHERE id=?", "c"); getErr != nil {
				return getErr
			}
			assert.Equal(t, widget{ID: "c", Label: "Gamma"}, w)
			var all []widget
			if selErr := db.Select(ctx, &all, "SELECT id,label FROM widgets ORDER BY id"); selErr != nil {
				return selErr
			}
			assert.Len(t, all, 3)
			return rollback
		})
		require.ErrorIs(t, err, rollback)
		var count int
		require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM widgets"))
		assert.Equal(t, 2, count, "Get/Select must use the rolled-back transaction")
	})

	t.Run("propagate cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var w widget
		require.ErrorIs(t, db.Get(ctx, &w, "SELECT id,label FROM widgets WHERE id=?", "a"), context.Canceled)
		var widgets []widget
		require.ErrorIs(t, db.Select(ctx, &widgets, "SELECT id,label FROM widgets"), context.Canceled)
	})
}

func TestPragmasOnEveryPooledConnection(t *testing.T) {
	db, err := OpenSQLite(t.Context(), t.TempDir())
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
	db, err := OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	_, err = db.Exec(
		t.Context(),
		"INSERT INTO nodes(id, hostname, payload) VALUES ('node-1', 'node-1', '{}')",
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var count int
	require.NoError(t, db.Get(
		t.Context(),
		&count,
		"SELECT count(*) FROM nodes WHERE id = 'node-1'",
	))
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

	db, err := OpenSQLite(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, name := range legacyFiles {
		payload, readErr := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, readErr)
		assert.Equal(t, "{invalid legacy json", string(payload), "legacy files must remain untouched")
	}
	var importedRows int
	require.NoError(t, db.Get(t.Context(), &importedRows, `
		SELECT
			(SELECT count(*) FROM deployments) +
			(SELECT count(*) FROM event_history) +
			(SELECT count(*) FROM alerts) +
			(SELECT count(*) FROM recommendations)
	`))
	assert.Zero(t, importedRows, "legacy files must not create SQLite records")
	var legacyTableCount int
	require.NoError(t, db.Get(
		t.Context(), &legacyTableCount, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='legacy_imports'",
	))
	assert.Zero(t, legacyTableCount)
}
