package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/knownapp"
)

func TestStoreGet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "services.json")
	ctx := context.Background()
	store, err := NewStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err)

	require.NoError(t, store.ReplaceStack(ctx, "payments", []Info{
		{
			Name:  " api ",
			Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
			Metadata: metadata.Metadata{
				Description: " Payments API ",
			},
			Environment: map[string]string{
				"APP_ENV": "prod",
			},
		},
	}))
	require.NoError(t, store.ReplaceStack(ctx, "infra", []Info{
		{
			Name:  "proxy",
			Image: "ghcr.io/swarm-deploy/proxy:v4.5.6",
		},
	}))

	info, ok := store.Get(" payments ", " api ")
	require.True(t, ok)
	assert.Equal(t, "payments", info.Stack)
	assert.Equal(t, " api ", info.Name)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", info.Image)
	assert.Equal(t, "", info.Spec.Image)
	assert.Equal(t, " Payments API ", info.Description)
	assert.Equal(t, map[string]string{"APP_ENV": "prod"}, info.Environment)
}

func TestStoreGetReturnsFalseWhenServiceNotFound(t *testing.T) {
	t.Parallel()

	store, err := NewStore(context.Background(), filepath.Join(t.TempDir(), "services.json"), fs.NewLocalFileSystem())
	require.NoError(t, err)

	_, ok := store.Get("payments", "api")
	assert.False(t, ok)
}

func TestStoreGetRestoresIndexOnReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "services.json")
	ctx := context.Background()
	store, err := NewStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err)

	require.NoError(t, store.ReplaceStack(ctx, "payments", []Info{
		{
			Name:  "api",
			Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
			Metadata: metadata.Metadata{
				KnownApp: knownapp.NginxProxy,
			},
			Environment: map[string]string{
				"APP_ENV": "prod",
			},
		},
	}))

	reloaded, err := NewStore(ctx, path, fs.NewLocalFileSystem())
	require.NoError(t, err)

	info, ok := reloaded.Get("payments", "api")
	require.True(t, ok)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", info.Image)
	assert.Equal(t, knownapp.NginxProxy, info.KnownApp)
	assert.Equal(t, map[string]string{"APP_ENV": "prod"}, info.Environment)
}
