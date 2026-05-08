package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestNodeStoreReplaceAndLoad(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nodes.json")

	store, err := NewNodeStore(storePath)
	require.NoError(t, err, "create store")

	err = store.Replace([]swarm.Node{
		{
			ID:            "b",
			Hostname:      "node-b",
			Status:        "ready",
			Availability:  "active",
			ManagerStatus: "worker",
			EngineVersion: "28.3.0",
			Addr:          "10.0.0.2",
			CPUNano:       4_000_000_000,
			MemoryBytes:   17_179_869_184,
		},
		{
			ID:            "a",
			Hostname:      "node-a",
			Status:        "ready",
			Availability:  "active",
			ManagerStatus: "leader",
			EngineVersion: "28.3.0",
			Addr:          "10.0.0.1",
			CPUNano:       8_000_000_000,
			MemoryBytes:   34_359_738_368,
		},
	})
	require.NoError(t, err, "replace nodes")

	snapshot := store.List()
	require.Len(t, snapshot, 2, "expected two persisted nodes")
	assert.Equal(t, "a", snapshot[0].ID, "expected sorting by hostname then id")
	assert.Equal(t, "b", snapshot[1].ID, "expected sorting by hostname then id")
	assert.Equal(t, "node-a", snapshot[0].Hostname, "expected hostname normalization")
	assert.Equal(t, "10.0.0.1", snapshot[0].Addr, "expected addr normalization")

	reloaded, err := NewNodeStore(storePath)
	require.NoError(t, err, "reload store")
	assert.Equal(t, snapshot, reloaded.List(), "expected persisted snapshot")
}

func TestNodeStoreLoadFailsOnInvalidJSON(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nodes.json")
	require.NoError(t, os.WriteFile(storePath, []byte("{"), 0o600), "write broken payload")

	_, err := NewNodeStore(storePath)
	require.Error(t, err, "expected decode error")
	assert.Contains(t, err.Error(), "decode nodes file", "unexpected error")
}

func TestNodeStoreListReturnsCopy(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nodes.json")
	store, err := NewNodeStore(storePath)
	require.NoError(t, err, "create store")

	err = store.Replace([]swarm.Node{
		{ID: "node-1", Hostname: "node-1"},
	})
	require.NoError(t, err, "replace nodes")

	snapshot := store.List()
	snapshot[0].Hostname = "changed"

	next := store.List()
	assert.Equal(t, "node-1", next[0].Hostname, "list must return a copy")
}
