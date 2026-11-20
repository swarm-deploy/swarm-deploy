package conversation

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLChatUsageDistinguishesMissingAndZero(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)
	now := time.Date(2026, 4, 5, 6, 7, 8, 9, time.UTC)

	require.NoError(t, repo.Save(t.Context(), Chat{
		ID: "missing", Title: "Missing", CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.Save(t.Context(), Chat{
		ID: "zero", Title: "Zero", CreatedAt: now, UpdatedAt: now, Usage: &TokenUsage{},
	}))

	missing, exists, err := repo.ReadChat(t.Context(), "missing")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Nil(t, missing.Usage)
	zero, exists, err := repo.ReadChat(t.Context(), "zero")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, &TokenUsage{}, zero.Usage)

	var rows []struct {
		ID    string        `db:"id"`
		Input sql.NullInt64 `db:"input_tokens"`
	}
	require.NoError(t, db.Select(t.Context(), &rows,
		"SELECT id,input_tokens FROM assistant_chats ORDER BY id"))
	require.Len(t, rows, 2)
	assert.False(t, rows[0].Input.Valid)
	assert.True(t, rows[1].Input.Valid)
	assert.Zero(t, rows[1].Input.Int64)
}

func TestSQLChatTransactionsAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
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
	db, err = storage.OpenSQLite(ctx, dir)
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
	_, err = db.Exec(ctx, "CREATE TRIGGER reject_turn BEFORE INSERT ON assistant_turns BEGIN SELECT RAISE(ABORT, 'injected turn failure'); END")
	require.NoError(t, err)
	require.Error(t, repo.SaveTurns(ctx, "chat", TokenUsage{TotalTokens: 100}, Turn{Role: "assistant", Content: "lost"}))
	unchanged, exists, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, chat, unchanged)

	buckets, err := repo.ReadUsage(ctx, chat.CreatedAt.UTC().AddDate(0, 0, -1), time.Now().UTC().AddDate(0, 0, 1), UsageDaily)
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, *chat.Usage, buckets[0].TokenUsage)
}

func TestSQLChatConcurrentAppends(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
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

func TestSQLAssistantUsageCalendarBuckets(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)

	date := func(value string) time.Time {
		parsed, err := time.Parse(time.DateOnly, value)
		require.NoError(t, err)
		return parsed
	}
	for _, sample := range []struct {
		at    time.Time
		usage TokenUsage
	}{
		{date("2026-11-01"), TokenUsage{InputTokens: 100}},
		{date("2026-11-15").Add(23 * time.Hour).In(time.FixedZone("offset", 3*60*60)), TokenUsage{5, 2, 8}},
		{date("2026-11-17"), TokenUsage{9, 3, 13}},
		{date("2026-12-02"), TokenUsage{8, 2, 10}},
		{date("2026-12-03"), TokenUsage{OutputTokens: 100}},
	} {
		require.NoError(t, repo.addDailyUsage(ctx, sample.at, sample.usage))
	}

	for _, tc := range []struct {
		name     string
		interval UsageInterval
		from, to string
		want     []UsageBucket
	}{
		{"daily", UsageDaily, "2026-11-15", "2026-11-18", []UsageBucket{
			{date("2026-11-15"), TokenUsage{5, 2, 8}},
			{date("2026-11-16"), TokenUsage{}},
			{date("2026-11-17"), TokenUsage{9, 3, 13}},
		}},
		{"weekly", UsageWeekly, "2026-11-15", "2026-12-03", []UsageBucket{
			{date("2026-11-09"), TokenUsage{5, 2, 8}},
			{date("2026-11-16"), TokenUsage{9, 3, 13}},
			{date("2026-11-23"), TokenUsage{}},
			{date("2026-11-30"), TokenUsage{8, 2, 10}},
		}},
		{"monthly", UsageMonthly, "2026-11-15", "2026-12-03", []UsageBucket{
			{date("2026-11-01"), TokenUsage{14, 5, 21}},
			{date("2026-12-01"), TokenUsage{8, 2, 10}},
		}},
		{"before recorded usage", UsageWeekly, "2026-11-02", "2026-11-15", []UsageBucket{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ReadUsage(t.Context(), date(tc.from), date(tc.to), tc.interval)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
