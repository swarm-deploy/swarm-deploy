package conversation

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLChatTransactionsAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	repo := NewSQLHistoryStorage(db)
	for _, id := range []string{"", "  "} {
		require.Error(t, repo.SaveTurns(ctx, id, TokenUsage{}, Turn{Role: "user", Content: "hello"}))
	}
	_, exists, err := repo.ReadChat(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)
	require.NoError(t, repo.SaveTurns(ctx, "chat", TokenUsage{InputTokens: 2, TotalTokens: 2}, Turn{Role: "user", Content: "Hello"}))
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.SaveTurns(ctx, "chat", TokenUsage{OutputTokens: 5, TotalTokens: 5}, Turn{Role: "assistant", Content: "rollback"}))
		return assert.AnError
	}), assert.AnError)
	require.NoError(t, db.Close())
	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	repo = NewSQLHistoryStorage(db)
	chat, exists, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, exists)
	require.Len(t, chat.Turns, 1)
	assert.EqualValues(t, 2, chat.Usage.TotalTokens)
	summaries, err := repo.ReadChats(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, chat.Title, summaries[0].Title)
	// Failure after metadata was updated must also restore metadata and original turns.
	_, err = db.Get(ctx).ExecContext(ctx, "CREATE TRIGGER reject_turn BEFORE INSERT ON assistant_turns BEGIN SELECT RAISE(ABORT, 'injected turn failure'); END")
	require.NoError(t, err)
	require.Error(t, repo.SaveTurns(ctx, "chat", TokenUsage{TotalTokens: 100}, Turn{Role: "assistant", Content: "lost"}))
	unchanged, exists, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, chat, unchanged)
}

func TestSQLChatConcurrentAppends(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := NewSQLHistoryStorage(db)
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			errs <- repo.SaveTurns(ctx, "chat", TokenUsage{TotalTokens: 1}, Turn{Role: "user", Content: "hello"})
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	chat, exists, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Len(t, chat.Turns, 8)
	assert.EqualValues(t, 8, chat.Usage.TotalTokens)
}
