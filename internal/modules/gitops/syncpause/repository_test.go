package syncpause

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestRepositoryPersistsNullableStackPauseAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	repo := NewRepository(db)

	changed, err := repo.Upsert(ctx, Pause{StackName: "app"})
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = repo.Upsert(ctx, Pause{StackName: "app"})
	require.NoError(t, err)
	assert.False(t, changed, "same expiration is idempotent")
	require.NoError(t, db.Close())

	db, err = storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	policy, err := NewRepository(db).Active(ctx, "app", time.Now())
	require.NoError(t, err)
	require.NotNil(t, policy.StackPause)
	assert.Nil(t, policy.StackPause.ExpiresAt, "NULL means no expiration")
}
