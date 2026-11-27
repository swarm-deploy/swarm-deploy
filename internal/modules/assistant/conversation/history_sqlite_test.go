package conversation

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	sqlitemigrations "github.com/swarm-deploy/swarm-deploy/migrations/sqlite"
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
		_, err = repo.SaveTurns(ctx, id, SaveOptions{}, TokenUsage{}, Turn{Role: "user", Content: "hello"})
		require.Error(t, err)
	}
	_, exists, err := repo.ReadChat(ctx, "missing")
	require.NoError(t, err)
	assert.False(t, exists)
	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{InputTokens: 2, TotalTokens: 2}, Turn{Role: "user", Content: "Hello"})
	require.NoError(t, err)
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{OutputTokens: 5, TotalTokens: 5}, Turn{Role: "assistant", Content: "rollback"})
		require.NoError(t, err)
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
	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{TotalTokens: 100}, Turn{Role: "assistant", Content: "lost"})
	require.Error(t, err)
	unchanged, exists, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, chat, unchanged)
}

func TestSQLReadOnlyChatKeepsModeWhenAppendingTurns(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)

	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{CreatedBy: "mcp", ReadOnly: true}, TokenUsage{},
		Turn{Role: "user", Content: "Check the cluster"})
	require.NoError(t, err)
	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{},
		Turn{Role: "assistant", Content: "The cluster is healthy"})
	require.NoError(t, err)
	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{},
		Turn{Role: "user", Content: "Restart api"})
	require.NoError(t, err)

	chat, found, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, chat.ReadOnly)
	assert.Equal(t, "mcp", chat.CreatedBy)
	require.Len(t, chat.Turns, 3)

	summaries, err := repo.ReadChats(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.True(t, summaries[0].ReadOnly)
}

func TestSQLChatAppendsAfterLastSequenceWithoutReplacingHistory(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)
	require.NoError(t, repo.Append(ctx, "chat", Turn{Role: "user", Content: "Original title"}))
	_, err = db.Exec(ctx, "UPDATE assistant_turns SET sequence = 5 WHERE chat_id = 'chat'")
	require.NoError(t, err)
	_, err = db.Exec(ctx, "CREATE TRIGGER keep_history BEFORE DELETE ON assistant_turns BEGIN SELECT RAISE(ABORT, 'unexpected delete'); END")
	require.NoError(t, err)

	_, err = repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{TotalTokens: 2},
		Turn{Role: "assistant", Content: "First reply"}, Turn{Role: "user", Content: "Follow-up"})
	require.NoError(t, err)
	var sequences []int
	require.NoError(t, db.Select(ctx, &sequences, "SELECT sequence FROM assistant_turns WHERE chat_id = ? ORDER BY sequence", "chat"))
	assert.Equal(t, []int{5, 6, 7}, sequences)
	chat, found, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Original title", chat.Title)
	assert.Equal(t, &TokenUsage{TotalTokens: 2}, chat.Usage)
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
			_, err := repo.SaveTurns(ctx, "chat", SaveOptions{}, TokenUsage{TotalTokens: 1}, Turn{Role: "user", Content: "hello"})
			errs <- err
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

func TestSQLChatReadDoesNotWaitForWriter(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)
	require.NoError(t, repo.Append(ctx, "chat", Turn{Role: "user", Content: "Original"}))

	writerStarted := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseWriter) }) }
	defer release()
	go func() {
		writerDone <- db.WithinTransaction(ctx, func(ctx context.Context) error {
			_, updateErr := db.Exec(ctx, "UPDATE assistant_chats SET title = ? WHERE id = ?", "Pending", "chat")
			if updateErr != nil {
				return updateErr
			}
			close(writerStarted)
			<-releaseWriter
			return assert.AnError
		})
	}()
	select {
	case <-writerStarted:
	case writerErr := <-writerDone:
		require.NoError(t, writerErr)
	case <-time.After(time.Second):
		require.Fail(t, "writer did not start")
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	chat, found, err := repo.ReadChat(readCtx, "chat")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Original", chat.Title)
	assert.Equal(t, []Turn{{Sequence: 0, Role: "user", Content: "Original"}}, chat.Turns)

	release()
	require.ErrorIs(t, <-writerDone, assert.AnError)
}

func TestSQLChatPrimaryArtifactSelection(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)

	branchA := Artifact{Type: ArtifactTypeGitBranch, Label: "branch-a", URL: "https://example.com/branch-a"}
	pullRequest42 := Artifact{Type: ArtifactTypePullRequest, Label: "PR #42", URL: "https://example.com/pull/42"}
	branchB := Artifact{Type: ArtifactTypeGitBranch, Label: "branch-b"}
	pullRequest43 := Artifact{Type: ArtifactTypePullRequest, Label: "PR #43", URL: "https://example.com/pull/43"}

	steps := []struct {
		name string
		turn Turn
		want Artifact
	}{
		{name: "branch A", turn: Turn{Role: "assistant", Content: "A", Artifacts: []Artifact{branchA}}, want: branchA},
		{name: "PR 42", turn: Turn{Role: "assistant", Content: "PR", Artifacts: []Artifact{pullRequest42}}, want: pullRequest42},
		{name: "ordinary reply", turn: Turn{Role: "assistant", Content: "No artifact"}, want: pullRequest42},
		{name: "branch B", turn: Turn{Role: "assistant", Content: "B", Artifacts: []Artifact{branchB}}, want: pullRequest42},
		{name: "PR 43", turn: Turn{Role: "assistant", Content: "New PR", Artifacts: []Artifact{pullRequest43}}, want: pullRequest43},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			require.NoError(t, repo.Append(ctx, "chat", step.turn))
			summaries, readErr := repo.ReadChats(ctx)
			require.NoError(t, readErr)
			require.Len(t, summaries, 1)
			assert.Equal(t, &step.want, summaries[0].PrimaryArtifact)
		})
	}

	chat, found, err := repo.ReadChat(ctx, "chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, chat.Turns, len(steps))
	assert.Equal(t, []Artifact{branchA}, chat.Turns[0].Artifacts)
	assert.Equal(t, &pullRequest43, chat.PrimaryArtifact)
}

func TestSQLChatFullSaveRebuildsPrimaryArtifact(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)
	now := time.Now().UTC()
	pullRequest := Artifact{Type: ArtifactTypePullRequest, Label: "PR #42", URL: "https://example.com/pull/42"}

	require.NoError(t, repo.Save(ctx, Chat{
		ID: "chat", Title: "Artifacts", CreatedAt: now, UpdatedAt: now,
		PrimaryArtifact: &Artifact{Type: ArtifactTypeGitBranch, Label: "stale"},
		Turns: []Turn{
			{Role: "assistant", Content: "Branch", Artifacts: []Artifact{{Type: ArtifactTypeGitBranch, Label: "branch"}}},
			{Role: "assistant", Content: "PR", Artifacts: []Artifact{pullRequest}},
			{Role: "assistant", Content: "Later branch", Artifacts: []Artifact{{Type: ArtifactTypeGitBranch, Label: "later"}}},
		},
	}))

	summaries, err := repo.ReadChats(ctx)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, &pullRequest, summaries[0].PrimaryArtifact)
}

func TestSQLChatPreservesCreatorFromFirstInsert(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)

	for _, tc := range []struct {
		id        string
		createdBy string
		want      sql.NullString
	}{
		{id: "authenticated", createdBy: "alice", want: sql.NullString{String: "alice", Valid: true}},
		{id: "anonymous"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			_, err = repo.SaveTurns(ctx, tc.id, SaveOptions{CreatedBy: tc.createdBy}, TokenUsage{InputTokens: 1, TotalTokens: 1})
			require.NoError(t, err)
			_, err = repo.SaveTurns(ctx, tc.id, SaveOptions{CreatedBy: "bob"}, TokenUsage{}, Turn{Role: "user", Content: "hello"})
			require.NoError(t, err)

			var createdBy sql.NullString
			require.NoError(t, db.Get(ctx, &createdBy, "SELECT created_by FROM assistant_chats WHERE id = ?", tc.id))
			assert.Equal(t, tc.want, createdBy)
		})
	}
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
	for i, sample := range []struct {
		createdAt time.Time
		usage     *TokenUsage
	}{
		{date("2026-11-01"), &TokenUsage{InputTokens: 100}},
		{date("2026-11-02"), nil},
		{date("2026-11-15").Add(23 * time.Hour).In(time.FixedZone("offset", 3*60*60)), &TokenUsage{2, 1, 4}},
		{date("2026-11-15"), &TokenUsage{3, 1, 4}},
		{date("2026-11-17"), &TokenUsage{9, 3, 13}},
		{date("2026-12-02"), &TokenUsage{8, 2, 10}},
		{date("2026-12-03"), &TokenUsage{OutputTokens: 100}},
	} {
		require.NoError(t, repo.Save(ctx, Chat{
			ID: strconv.Itoa(i), Title: "Chat", CreatedAt: sample.createdAt,
			UpdatedAt: date("2026-12-04"), Usage: sample.usage,
		}))
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
		{"only chats without usage", UsageWeekly, "2026-11-02", "2026-11-15", []UsageBucket{}},
		{"only chat update dates in range", UsageDaily, "2026-12-04", "2026-12-05", []UsageBucket{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.ReadUsage(t.Context(), date(tc.from), date(tc.to), tc.interval)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSQLChatUnread(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	repo := NewSQLHistoryStorage(db)

	for _, step := range []struct {
		name string
		role string
		mark *int
		want bool
	}{
		{name: "first question", role: "user"},
		{name: "first reply", role: "assistant", want: true},
		{name: "later question preserves unread", role: "user", want: true},
		{name: "user sequence cannot clear unread", mark: new(2), want: true},
		{name: "mark latest assistant reply even after a newer question", mark: new(1)},
		{name: "repeat acknowledgement", mark: new(1)},
		{name: "second reply", role: "assistant", want: true},
		{name: "delayed acknowledgement", mark: new(1), want: true},
		{name: "future sequence cannot clear unread", mark: new(4), want: true},
		{name: "mark second reply", mark: new(3)},
		{name: "later question preserves read", role: "user"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.mark == nil {
				require.NoError(t, repo.Append(ctx, "chat", Turn{Role: step.role, Content: step.name}))
			} else {
				before, found, err := repo.ReadChat(ctx, "chat")
				require.NoError(t, err)
				require.True(t, found)
				require.NoError(t, repo.MarkRead(ctx, "chat", *step.mark))
				after, found, err := repo.ReadChat(ctx, "chat")
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
				assert.Equal(t, before.Turns, after.Turns)
			}
			chats, err := repo.ReadChats(ctx)
			require.NoError(t, err)
			require.Len(t, chats, 1)
			assert.Equal(t, step.want, chats[0].Unread)
		})
	}
}

func TestSQLChatUnreadMigration(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "swarm-deploy.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = legacy.Close() })
	migrations, err := goose.NewProvider(goose.DialectSQLite3, legacy, sqlitemigrations.Files)
	require.NoError(t, err)
	_, err = migrations.UpTo(ctx, 8)
	require.NoError(t, err)
	now := time.Now().UnixNano()
	_, err = legacy.ExecContext(ctx, "INSERT INTO assistant_chats (id, title, created_at, updated_at) VALUES ('old', 'Old', ?, ?)", now, now)
	require.NoError(t, err)
	_, err = legacy.ExecContext(ctx, `INSERT INTO assistant_turns (chat_id, sequence, payload) VALUES ('old', 7, '{"role":"assistant","content":"Existing reply"}')`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	chat, found, err := NewSQLHistoryStorage(db).ReadChat(ctx, "old")
	require.NoError(t, err)
	require.True(t, found)
	assert.False(t, chat.Unread)
	require.Len(t, chat.Turns, 1)
	assert.Equal(t, 7, chat.Turns[0].Sequence)
	assert.Equal(t, "Existing reply", chat.Turns[0].Content)
	assert.Empty(t, chat.Turns[0].Artifacts)
	assert.Nil(t, chat.PrimaryArtifact)
}
