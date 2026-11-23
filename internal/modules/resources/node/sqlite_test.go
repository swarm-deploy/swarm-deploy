package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestNodeStorePhysicalContentAndRoundTrip(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)

	empty, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Empty(t, empty)

	full := swarm.Node{
		ID: "n2", Hostname: "alpha", Status: "ready", Availability: "active",
		ManagerStatus: swarm.NodeManagerStatusWorker, EngineVersion: "28", Addr: "10.0.0.2",
		CPUNano: 4, MemoryBytes: 8, Labels: map[string]string{"zone": "a"},
	}
	sparse := swarm.Node{ID: "n1", Hostname: "beta"}
	require.NoError(t, store.ReplaceSnapshot(t.Context(), []swarm.Node{sparse, full}))

	nodes, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []swarm.Node{full, sparse}, nodes, "sorted by hostname")
	lookup, err := store.Lookup(t.Context())
	require.NoError(t, err)
	assert.Equal(t, full, lookup["n2"])

	var row struct {
		ID          string `db:"id"`
		Hostname    string `db:"hostname"`
		HasID       int    `db:"has_id"`
		HasHostname int    `db:"has_hostname"`
	}
	require.NoError(t, db.Get(t.Context(), &row, `SELECT id,hostname,
		json_type(payload,'$.id') IS NOT NULL AS has_id,
		json_type(payload,'$.hostname') IS NOT NULL AS has_hostname
		FROM nodes WHERE id='n2'`))
	assert.Equal(t, "n2", row.ID)
	assert.Equal(t, "alpha", row.Hostname)
	assert.Zero(t, row.HasID)
	assert.Zero(t, row.HasHostname)

	var payload string
	require.NoError(t, db.Get(t.Context(), &payload, "SELECT payload FROM nodes WHERE id='n2'"))
	assert.JSONEq(t, `{"status":"ready","availability":"active","manager_status":"worker","engine_version":"28",
		"addr":"10.0.0.2","cpu_nano":4,"memory_bytes":8,"labels":{"zone":"a"}}`, payload)

	var identities int
	require.NoError(t, db.Get(t.Context(), &identities, "SELECT count(*) FROM node_identities"))
	assert.Equal(t, 2, identities)

	require.NoError(t, store.ReplaceSnapshot(t.Context(), nil))
	nodes, err = store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
	require.NoError(t, db.Get(t.Context(), &identities, "SELECT count(*) FROM node_identities"))
	assert.Equal(t, 2, identities, "identities survive snapshot replacement")
}

func TestNodeStoreReplaceRollsBackOnDuplicate(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := NewSQLStore(db)
	require.NoError(t, store.ReplaceSnapshot(t.Context(), []swarm.Node{{ID: "keep", Hostname: "h"}}))

	require.Error(t, store.ReplaceSnapshot(t.Context(), []swarm.Node{{ID: "d", Hostname: "x"}, {ID: "d", Hostname: "y"}}))
	nodes, err := store.ReadAll(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []swarm.Node{{ID: "keep", Hostname: "h"}}, nodes)
}
