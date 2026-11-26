package handlers

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
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

func TestAssistantUsageHTTP(t *testing.T) {
	ctx := t.Context()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	history := conversation.NewSQLHistoryStorage(db)
	now := time.Now().UTC()
	_, err = history.SaveTurns(ctx, "first-chat", conversation.SaveOptions{}, conversation.TokenUsage{InputTokens: 12, OutputTokens: 3, TotalTokens: 16})
	require.NoError(t, err)
	_, err = history.SaveTurns(ctx, "second-chat", conversation.SaveOptions{}, conversation.TokenUsage{InputTokens: 6, OutputTokens: 2, TotalTokens: 8})
	require.NoError(t, err)
	service, err := assistant.NewService(assistant.Config{
		Localizer: newTestAssistantTexts(t, "en"),
		Enabled:   true, ConversationInMemoryTTL: time.Hour, ConversationHistory: history,
	}, nil, nil, dispatcher.NewOutboxDispatcher(outbox.New(db)), &metrics.NopAssistant{})
	require.NoError(t, err)
	h := &handler{assistant: service, assistantTexts: newTestAssistantTexts(t, "en")}
	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)

	recent := "from=" + now.AddDate(0, 0, -1).Format(time.DateOnly) + "&to=" + now.AddDate(0, 0, 2).Format(time.DateOnly)
	tests := []struct {
		name   string
		query  string
		status int
	}{
		{"recorded usage", recent + "&interval=daily", http.StatusOK},
		{"no usage", "from=2020-01-01&to=2020-02-01&interval=monthly", http.StatusOK},
		{"missing interval", recent, http.StatusBadRequest},
		{"unknown interval", recent + "&interval=yearly", http.StatusBadRequest},
		{"malformed date", "from=2026-02-30&to=2026-03-07&interval=weekly", http.StatusBadRequest},
		{"inverted dates", "from=2026-04-01&to=2026-03-01&interval=weekly", http.StatusBadRequest},
		{"equal dates", "from=2026-04-01&to=2026-04-01&interval=weekly", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/assistant/usage?"+tc.query, nil)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.name == "no usage" {
				assert.JSONEq(t, `{"buckets":[]}`, response.Body.String())
			}
			if tc.name == "recorded usage" {
				var payload struct {
					Buckets []struct {
						Start  string `json:"start"`
						Input  int64  `json:"input_tokens"`
						Output int64  `json:"output_tokens"`
						Total  int64  `json:"total_tokens"`
					} `json:"buckets"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
				require.NotEmpty(t, payload.Buckets)
				var total conversation.TokenUsage
				for _, bucket := range payload.Buckets {
					_, err := time.Parse(time.DateOnly, bucket.Start)
					require.NoError(t, err)
					total.Add(conversation.TokenUsage{InputTokens: bucket.Input, OutputTokens: bucket.Output, TotalTokens: bucket.Total})
				}
				assert.Equal(t, conversation.TokenUsage{InputTokens: 18, OutputTokens: 5, TotalTokens: 24}, total)
			}
		})
	}
}
