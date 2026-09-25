package conversation

import (
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

	chats, err := storage.List()
	require.NoError(t, err, "list chats")
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

func TestFileHistoryStorageKeepsConversationIDOutOfFilePath(t *testing.T) {
	dir := t.TempDir()
	storage, err := NewFileHistoryStorage(dir)
	require.NoError(t, err, "create history storage")

	require.NoError(t, storage.Append("../../escape", Turn{Role: "user", Content: "hello"}), "append chat")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read history directory")
	require.Len(t, entries, 1, "expected one history file")
	assert.Equal(t, ".json", filepath.Ext(entries[0].Name()), "history must stay inside storage directory")
}
