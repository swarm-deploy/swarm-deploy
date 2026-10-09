package spike

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Thiht/transactor"
	"github.com/Thiht/transactor/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setup(t *testing.T) (*sql.DB, transactor.Transactor, stdlib.DBGetter) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "test.sqlite")}
	u.RawQuery = "_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(4)
	_, err = db.Exec("CREATE TABLE entries (id TEXT PRIMARY KEY)")
	require.NoError(t, err)
	tr, getter := stdlib.NewTransactor(db, stdlib.NestedTransactionsSavepoints)
	return db, tr, getter
}

func count(t *testing.T, db stdlib.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&n))
	return n
}

func TestTransactionBoundaries(t *testing.T) {
	sentinel := errors.New("rollback requested")
	for _, tc := range []struct {
		name     string
		innerErr error
		outerErr error
		want     int
	}{
		{"commit", nil, nil, 2},
		{"inner rollback keeps outer", sentinel, nil, 1},
		{"outer rollback includes inner commit", nil, sentinel, 0},
		{"both roll back", sentinel, sentinel, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, tr, get := setup(t)
			err := tr.WithinTransaction(t.Context(), func(ctx context.Context) error {
				assert.True(t, stdlib.IsWithinTransaction(ctx))
				// The library returns its savepoint wrapper, not a concrete *sql.Tx.
				assert.NotEqual(t, get(context.Background()), get(ctx))
				_, err := get(ctx).ExecContext(ctx, "INSERT INTO entries VALUES ('outer')")
				if err != nil {
					return err
				}
				assert.Zero(t, count(t, db))
				innerErr := tr.WithinTransaction(ctx, func(inner context.Context) error {
					_, err := get(inner).ExecContext(inner, "INSERT INTO entries VALUES ('inner')")
					if err != nil {
						return err
					}
					return tc.innerErr
				})
				require.ErrorIs(t, innerErr, tc.innerErr)
				return tc.outerErr
			})
			require.ErrorIs(t, err, tc.outerErr)
			assert.Equal(t, tc.want, count(t, db))
		})
	}
}

func TestConcurrentTransactions(t *testing.T) {
	db, tr, get := setup(t)
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := range 16 {
		wg.Go(func() {
			results <- tr.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, err := get(ctx).ExecContext(ctx, "INSERT INTO entries VALUES (?)", fmt.Sprint(i))
				return err
			})
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assert.Equal(t, 16, count(t, db))
}

func TestPragmasOnEveryConnection(t *testing.T) {
	db, _, _ := setup(t)
	for range 4 {
		conn, err := db.Conn(t.Context())
		require.NoError(t, err)
		defer conn.Close()
		var foreignKeys, busyTimeout int
		var journal string
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busyTimeout))
		require.NoError(t, conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journal))
		assert.Equal(t, 1, foreignKeys)
		assert.Equal(t, 5000, busyTimeout)
		assert.Equal(t, "wal", journal)
	}
}
