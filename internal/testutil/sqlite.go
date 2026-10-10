// Package testutil contains test-only infrastructure helpers.
package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

// OpenSQLite opens an isolated database and registers cleanup.
func OpenSQLite(t *testing.T) *storage.Database {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
