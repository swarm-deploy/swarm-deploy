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
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	repo := NewSQLStore(db)
	require.NoError(t, repo.Update(ctx, func(s *model.Runtime) { s.GitRevision = "baseline" }))
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.Update(ctx, func(s *model.Runtime) { s.GitRevision = "rolled-back" }))
		return assert.AnError
	}), assert.AnError)
	require.NoError(t, db.Close())
	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	state, err := NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "baseline", state.GitRevision)
}

func TestSQLRuntimeConcurrentUpdatesAndWriteErrors(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
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
	_, err = db.Get(ctx).ExecContext(ctx, "CREATE TRIGGER reject_runtime BEFORE UPDATE ON gitops_runtime BEGIN SELECT RAISE(ABORT, 'injected write error'); END")
	require.NoError(t, err)
	require.Error(t, repo.Update(ctx, func(s *model.Runtime) { s.GitRevision = "lost" }))
	state, err = repo.Read(ctx)
	require.NoError(t, err)
	assert.Len(t, state.GitRevision, 12)
}
