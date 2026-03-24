package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	err = store.Replace([]NodeInfo{
		{ID: "node-1", Hostname: "node-1"},
	})
	require.NoError(t, err, "replace nodes")

	snapshot := store.List()
	snapshot[0].Hostname = "changed"

	next := store.List()
	assert.Equal(t, "node-1", next[0].Hostname, "list must return a copy")
}
