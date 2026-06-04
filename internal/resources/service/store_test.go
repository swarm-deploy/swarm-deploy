package service

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreGet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "services.json")
	store, err := NewStore(path)
	require.NoError(t, err)

	require.NoError(t, store.ReplaceStack("payments", []Info{
		{
			Name:        " api ",
			Image:       "ghcr.io/swarm-deploy/payments-api:v1.2.3",
			Description: " Payments API ",
		},
	}))
	require.NoError(t, store.ReplaceStack("infra", []Info{
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
}

func TestStoreGetReturnsFalseWhenServiceNotFound(t *testing.T) {
	t.Parallel()

	store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err)

	_, ok := store.Get("payments", "api")
	assert.False(t, ok)
}

func TestStoreGetRestoresIndexOnReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "services.json")
	store, err := NewStore(path)
	require.NoError(t, err)

	require.NoError(t, store.ReplaceStack("payments", []Info{
		{
			Name:  "api",
			Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
		},
	}))

	reloaded, err := NewStore(path)
	require.NoError(t, err)

	info, ok := reloaded.Get("payments", "api")
	require.True(t, ok)
	assert.Equal(t, "ghcr.io/swarm-deploy/payments-api:v1.2.3", info.Image)
}
