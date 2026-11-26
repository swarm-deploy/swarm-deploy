package assistant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestAssistantRunTracingSurvivesPolling(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	release := make(chan struct{})
	started := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			close(started)
			select {
			case <-release:
			case <-req.Context().Done():
				return
			}
			writeChatResponse(t, w, `{"route":"diagnostics","capabilities":["alert_management"],"operation":null}`, nil)
		case 2:
			writeChatResponse(t, w, "", []map[string]any{configurationChatCall("call-alert", "alert_list", `{}`)})
		default:
			writeChatResponse(t, w, "No alerts", nil)
		}
	}))
	defer server.Close()
	defer unblock()

	service := newConfigurationChatService(t, server.URL, configurationChatSpec())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	response := service.Chat(ctx, ChatRequest{Message: "Check active alerts", WaitTimeoutMS: 1})
	require.Equal(t, StatusInProgress, response.Status)
	cancel()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("assistant did not start")
	}

	var chatSpan, runSpan sdktrace.ReadWriteSpan
	for _, span := range recorder.Started() {
		switch span.Name() {
		case "assistant.Chat":
			chatSpan = span
		case "assistant.Run":
			runSpan = span
		}
	}
	require.NotNil(t, chatSpan)
	require.NotNil(t, runSpan)
	assert.Equal(t, chatSpan.SpanContext(), runSpan.Parent())
	for _, span := range recorder.Ended() {
		assert.NotEqual(t, runSpan.SpanContext(), span.SpanContext(), "run must outlive the initial chat call")
	}

	unblock()
	completed := service.Chat(t.Context(), ChatRequest{RequestID: response.RequestID, WaitTimeoutMS: 5000})
	require.Equal(t, StatusCompleted, completed.Status)
	require.Eventually(t, func() bool {
		for _, span := range recorder.Ended() {
			if span.SpanContext().SpanID() == runSpan.SpanContext().SpanID() {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	var endedChat, endedRun, toolSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch {
		case span.SpanContext().SpanID() == chatSpan.SpanContext().SpanID():
			endedChat = span
		case span.SpanContext().SpanID() == runSpan.SpanContext().SpanID():
			endedRun = span
		case span.Name() == "tool alert_list":
			toolSpan = span
		}
	}
	require.NotNil(t, endedChat)
	require.NotNil(t, endedRun)
	require.NotNil(t, toolSpan)
	assert.Equal(t, endedRun.SpanContext(), toolSpan.Parent())
	assert.True(t, endedChat.EndTime().Before(toolSpan.StartTime()))
	assert.False(t, endedRun.EndTime().Before(toolSpan.EndTime()))
	assert.Contains(t, endedRun.Attributes(), tracing.GenAIConversationID.String(response.ConversationID))
	assert.Contains(t, toolSpan.Attributes(), tracing.GenAIToolCallID.String("call-alert"))
}
