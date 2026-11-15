package syncpause

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestRepositoryPersistsNullableIndependentPausesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	repo := NewRepository(db)
	expires := time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)

	changed, err := repo.Upsert(ctx, Pause{Scope: ScopeStack, StackName: "app"})
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = repo.Upsert(ctx, Pause{Scope: ScopeService, StackName: "app", ServiceName: "api", ExpiresAt: &expires})
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = repo.Upsert(ctx, Pause{Scope: ScopeService, StackName: "app", ServiceName: "api", ExpiresAt: &expires})
	require.NoError(t, err)
	assert.False(t, changed, "same absolute expiration is idempotent")
	require.NoError(t, db.Close())

	db, err = storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	policy, err := NewRepository(db).Active(ctx, "app", expires.Add(-time.Hour))
	require.NoError(t, err)
	require.NotNil(t, policy.StackPause)
	assert.Nil(t, policy.StackPause.ExpiresAt, "NULL means no expiration")
	require.Contains(t, policy.ServicePauses, "api")
	require.NotNil(t, policy.ServicePauses["api"].ExpiresAt)
	assert.True(t, policy.ServicePauses["api"].ExpiresAt.Equal(expires))
}
