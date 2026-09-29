package modelstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestFileStoreReplacePersistsMetadataAndReloadsIndexes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "secrets.state.json")
	store, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err)

	now := time.Date(2026, time.September, 30, 1, 0, 0, 0, time.UTC)
	rows := []model.Secret{
		{
			ID:                "secret-b",
			Name:              "database-password",
			VersionID:         3,
			CreatedAt:         now.Add(-time.Hour),
			UpdatedAt:         now,
			Driver:            "vault",
			ExternalPath:      "kv/prod/database",
			ExternalVersionID: "v3",
			Managed:           true,
			Labels:            map[string]string{"environment": "production"},
		},
		{ID: "secret-a", Name: "api-key", VersionID: 1, CreatedAt: now, UpdatedAt: now},
	}
	require.NoError(t, store.Replace(ctx, rows))

	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "payload")
	assert.NotContains(t, string(payload), "secret_data")

	reloaded, err := NewFileStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err)
	listed, err := reloaded.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "api-key", listed[0].Name)

	secret, err := reloaded.GetByName(ctx, "database-password")
	require.NoError(t, err)
	assert.Equal(t, rows[0], secret)

	secret.Labels["environment"] = "changed"
	again, err := reloaded.GetByName(ctx, "database-password")
	require.NoError(t, err)
	assert.Equal(t, "production", again.Labels["environment"])
}

func TestFileStoreGetByNameReturnsNotFound(t *testing.T) {
	t.Parallel()

	store, err := NewFileStore(context.Background(), filepath.Join(t.TempDir(), "secrets.state.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)

	_, err = store.GetByName(context.Background(), "missing")
	require.ErrorIs(t, err, ErrSecretNotFound)
}
