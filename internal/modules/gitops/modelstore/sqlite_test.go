package modelstore

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLRuntimeRollbackAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	repo := NewSQLStore(db)
	require.NoError(t, repo.Update(ctx, func(s *model.Runtime) {
		s.GitRevision = "baseline"
		s.Stacks["app"] = model.Stack{Services: map[string]model.Service{
			"api": {
				DesiredDigest: "desired-digest", DesiredCommit: "desired-commit", SelfHealPending: true,
				SyncStatus: model.SyncStatusOutOfSync, SyncError: "Self Heal Convergence Failed",
			},
		}}
	}))
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.Update(ctx, func(s *model.Runtime) { s.GitRevision = "rolled-back" }))
		return assert.AnError
	}), assert.AnError)
	require.NoError(t, db.Close())
	db, err = storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	state, err := NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "baseline", state.GitRevision)
	assert.True(t, state.Stacks["app"].Services["api"].SelfHealPending)
	assert.Equal(t, "Self Heal Convergence Failed", state.Stacks["app"].Services["api"].SyncError)
	assert.Equal(t, "desired-digest", state.Stacks["app"].Services["api"].DesiredDigest)
	assert.Equal(t, "desired-commit", state.Stacks["app"].Services["api"].DesiredCommit)
}

func TestSQLRuntimeReadsLegacyServiceStateWithoutDesiredFields(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	legacy := `{"stacks":{"app":{"services":{"api":{"image":"api:1","sync_status":"Synced","sync_error":"","last_sync_at":"0001-01-01T00:00:00Z"}}}},"networks":{}}`
	_, err = db.Exec(t.Context(), "INSERT INTO gitops_runtime(id, payload) VALUES(1, ?)", legacy)
	require.NoError(t, err)

	state, err := NewSQLStore(db).Read(t.Context())
	require.NoError(t, err)
	assert.Empty(t, state.Stacks["app"].Services["api"].DesiredDigest)
	assert.Empty(t, state.Stacks["app"].Services["api"].DesiredCommit)
}

func TestSQLRuntimeConcurrentUpdatesAndWriteErrors(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := NewSQLStore(db)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() { errs <- repo.Update(ctx, func(s *model.Runtime) { s.GitRevision += "x" }) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	state, err := repo.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, state.GitRevision, 12)
	_, err = db.Exec(ctx, "CREATE TRIGGER reject_runtime BEFORE UPDATE ON gitops_runtime BEGIN SELECT RAISE(ABORT, 'injected write error'); END")
	require.NoError(t, err)
	require.Error(t, repo.Update(ctx, func(s *model.Runtime) { s.GitRevision = "lost" }))
	state, err = repo.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, state.GitRevision, 12)
}
