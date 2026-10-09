package storage

import (
	"context"
	"errors"
	"testing"

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
				_, err := db.Get(ctx).ExecContext(ctx, `INSERT INTO outbox_events(id,event_type,schema_version,occurred_at_ms,payload) VALUES (?,'nodeJoined',1,0,'{}')`, tc.id)
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
	require.NoError(t, db.Get(t.Context()).QueryRowContext(t.Context(), "SELECT id FROM outbox_events").Scan(&id))
	assert.Equal(t, "committed", id)
	var count int
	require.NoError(t, db.Get(t.Context()).QueryRowContext(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&count))
	assert.Equal(t, 1, count)
}

func TestRejectChangedSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(t.Context(), dir)
	require.NoError(t, err)
	_, err = db.Get(t.Context()).ExecContext(t.Context(), "UPDATE schema_migrations SET checksum='unexpected'")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), dir)
	require.Error(t, err)
	assert.Nil(t, db)
}
