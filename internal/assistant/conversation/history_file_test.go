package conversation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileHistoryStoragePersistsAndListsChats(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")

	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	storage.now = func() time.Time { return now }
	require.NoError(t, storage.Append("chat-1",
		Turn{Role: "user", Content: "Why is nginx restarting?"},
		Turn{Role: "assistant", Content: "Check the task history."},
	), "append first chat")

	now = now.Add(time.Minute)
	require.NoError(t, storage.Append("chat-2",
		Turn{Role: "user", Content: "Show me the cluster state"},
		Turn{Role: "assistant", Content: "The cluster is healthy."},
	), "append second chat")

	chats := storage.List()
	require.Len(t, chats, 2, "unexpected chat count")
	assert.Equal(t, "chat-2", chats[0].ID, "latest chat must be first")

	reopened, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "reopen history storage")
	chat, ok, err := reopened.Get("chat-1")
	require.NoError(t, err, "get persisted chat")
	require.True(t, ok, "expected persisted chat")
	require.Len(t, chat.Turns, 2, "unexpected persisted message count")
	assert.Equal(t, "Why is nginx restarting?", chat.Title, "unexpected persisted title")
}

func TestFileHistoryStoragePersistsTokenUsage(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")

	require.NoError(t, storage.AppendWithUsage(
		"chat-usage",
		TokenUsage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		Turn{Role: "user", Content: "hello"},
		Turn{Role: "assistant", Content: "hi"},
	), "append chat with usage")
	require.NoError(t, storage.AppendWithUsage(
		"chat-usage",
		TokenUsage{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		Turn{Role: "user", Content: "again"},
		Turn{Role: "assistant", Content: "hello again"},
	), "append second usage")

	chat, ok, err := storage.Get("chat-usage")
	require.NoError(t, err, "get chat")
	require.True(t, ok, "chat must exist")
	require.NotNil(t, chat.Usage, "usage must be persisted")
	assert.Equal(t, TokenUsage{InputTokens: 300, OutputTokens: 50, TotalTokens: 350}, *chat.Usage)

	chats := storage.List()
	require.Len(t, chats, 1)
	require.NotNil(t, chats[0].Usage)
	assert.Equal(t, TokenUsage{InputTokens: 300, OutputTokens: 50, TotalTokens: 350}, *chats[0].Usage)

	reopened, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "reopen history storage")
	chats = reopened.List()
	require.Len(t, chats, 1)
	require.NotNil(t, chats[0].Usage)
	assert.Equal(t, int64(350), chats[0].Usage.TotalTokens)
}

func TestFileHistoryStorageLoadsLegacyChatWithoutTokenUsage(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")
	require.NoError(t, storage.Append("legacy", Turn{Role: "user", Content: "hello"}))

	chatPath := storage.chatPath("legacy")
	payload, err := os.ReadFile(chatPath)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(payload, &raw))
	delete(raw, "token_usage")
	payload, err = json.Marshal(raw)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(chatPath, payload, chatFileMode))

	chat, ok, err := storage.Get("legacy")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Nil(t, chat.Usage)
}

func TestFileHistoryStorageKeepsConversationIDOutOfFilePath(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")

	require.NoError(t, storage.Append("../../escape", Turn{Role: "user", Content: "hello"}), "append chat")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read history directory")
	require.Len(t, entries, 2, "expected chat file and index")
	for _, entry := range entries {
		assert.Equal(t, ".json", filepath.Ext(entry.Name()), "history must stay inside storage directory")
	}
}

func TestFileHistoryStorageListUsesLoadedIndex(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")
	require.NoError(t, storage.Append("chat-1", Turn{Role: "user", Content: "hello"}), "append chat")

	chatPath := storage.chatPath("chat-1")
	require.NoError(t, os.Remove(chatPath), "remove chat payload after index is loaded")

	chats := storage.List()
	require.Len(t, chats, 1, "list must use in-memory index")
	assert.Equal(t, "chat-1", chats[0].ID, "unexpected chat id")
}

func TestFileHistoryStorageDoesNotRebuildMissingIndex(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")

	legacyChat := Chat{
		ID:        "legacy-chat",
		Title:     "Legacy chat",
		CreatedAt: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 26, 10, 1, 0, 0, time.UTC),
		Turns:     []Turn{{Role: "user", Content: "hello"}},
	}
	payload, err := json.Marshal(legacyChat)
	require.NoError(t, err, "encode legacy chat")
	require.NoError(t, os.WriteFile(storage.chatPath(legacyChat.ID), payload, chatFileMode), "write legacy chat file")

	reopened, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "reopen history storage without index")
	assert.Empty(t, reopened.List(), "missing index must not trigger chat file scan")
}
