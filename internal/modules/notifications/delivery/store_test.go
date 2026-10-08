package delivery

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestFileStoreTTLAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deliveries.json")
	filesystem := sharedfs.NewLocalFileSystem()
	now := time.Now()

	store, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "create store")
	store.now = func() time.Time { return now }
	for channel, id := range map[string]string{"tg": "1", "other": "2"} {
		require.NoError(t, store.Put(ctx, Correlation{
			CorrelationKey: "k", ChannelID: channel, Receipt: Receipt{MessageID: id}, ExpiresAt: now.Add(time.Hour),
		}), "put")
	}

	reloaded, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "reload store")
	reloaded.now = func() time.Time { return now }
	got, ok, err := reloaded.Get(ctx, "k", "tg")
	require.NoError(t, err, "get")
	require.True(t, ok, "correlation must survive restart")
	assert.Equal(t, "1", got.Receipt.MessageID, "keyed by (key, channel)")

	reloaded.now = func() time.Time { return now.Add(2 * time.Hour) }
	_, ok, err = reloaded.Get(ctx, "k", "tg")
	require.NoError(t, err, "get expired")
	assert.False(t, ok, "expired correlation must not be returned")

	require.NoError(t, reloaded.Delete(ctx, "k", "missing"), "deleting missing is not an error")
}

func TestFileStorePrunesExpiredOnWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deliveries.json")
	filesystem := sharedfs.NewLocalFileSystem()
	now := time.Now()

	store, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "create store")
	store.now = func() time.Time { return now }
	require.NoError(t, store.Put(ctx, Correlation{CorrelationKey: "old", ChannelID: "tg", ExpiresAt: now.Add(time.Hour)}), "put")

	store.now = func() time.Time { return now.Add(2 * time.Hour) }
	require.NoError(t, store.Put(ctx, Correlation{CorrelationKey: "new", ChannelID: "tg", ExpiresAt: now.Add(3 * time.Hour)}), "put")

	reloaded, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "reload")
	assert.Len(t, reloaded.items, 1, "expired correlation is removed from file")
}
