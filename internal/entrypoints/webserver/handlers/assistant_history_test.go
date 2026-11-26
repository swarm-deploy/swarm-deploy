package handlers

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestAssistantChatReadHTTP(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	history := conversation.NewSQLHistoryStorage(db)
	reply := conversation.Turn{Role: "assistant", Content: "Reply", Artifacts: []conversation.Artifact{{
		Type: conversation.ArtifactTypePullRequest, Label: "PR #42", URL: "https://example.com/pull/42",
	}}}
	_, err = history.SaveTurns(ctx, "chat", conversation.SaveOptions{ReadOnly: true}, conversation.TokenUsage{}, reply)
	require.NoError(t, err)
	service, err := assistant.NewService(assistant.Config{
		Localizer: newTestAssistantTexts(t, "en"),
		Enabled:   true, ConversationInMemoryTTL: time.Hour, ConversationHistory: history,
	}, nil, nil, dispatcher.NewOutboxDispatcher(outbox.New(db)), &metrics.NopAssistant{})
	require.NoError(t, err)
	h := &handler{assistant: service, assistantTexts: newTestAssistantTexts(t, "en")}
	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)

	const chatsPath = "/api/v1/assistant/chats"
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		require.Equal(t, status, res.Code, res.Body.String())
		return res.Body.Bytes()
	}
	readUnread := func(want bool) {
		t.Helper()
		var list generated.AssistantChatsResponse
		require.NoError(t, json.Unmarshal(request(http.MethodGet, chatsPath, "", http.StatusOK), &list))
		require.Len(t, list.Chats, 1)
		assert.Equal(t, want, list.Chats[0].Unread)
		assert.True(t, list.Chats[0].ReadOnly)
		primary, ok := list.Chats[0].PrimaryArtifact.Get()
		require.True(t, ok)
		assert.Equal(t, "PR #42", primary.Label)
	}

	var chat generated.AssistantChatHistory
	require.NoError(t, json.Unmarshal(request(http.MethodGet, chatsPath+"/chat", "", http.StatusOK), &chat))
	require.Len(t, chat.Messages, 1)
	assert.Zero(t, chat.Messages[0].Sequence)
	require.Len(t, chat.Messages[0].Artifacts, 1)
	assert.Equal(t, "PR #42", chat.Messages[0].Artifacts[0].Label)
	assert.True(t, chat.Unread)
	assert.True(t, chat.ReadOnly)
	readUnread(true)

	request(http.MethodPut, chatsPath+"/chat/read", `{}`, http.StatusBadRequest)
	request(http.MethodPut, chatsPath+"/chat/read", `{"sequence":-1}`, http.StatusBadRequest)
	readUnread(true)

	request(http.MethodPut, chatsPath+"/chat/read", `{"sequence":0}`, http.StatusNoContent)
	readUnread(false)
	require.NoError(t, history.Append(ctx, "chat", reply))
	request(http.MethodPut, chatsPath+"/chat/read", `{"sequence":0}`, http.StatusNoContent)
	readUnread(true)
}
