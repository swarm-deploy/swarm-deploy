package notifiers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskTelegramSendError(t *testing.T) {
	token := "12345:ABCDEF"
	err := errors.New(`Post "https://api.telegram.org/bot12345:ABCDEF/sendMessage": host unreachable`)

	masked := maskTelegramSendError(err, token)

	assert.NotContains(t, masked, token, "token must be masked")
	assert.Contains(t, masked, "/bot[REDACTED]/sendMessage", "telegram bot path must be redacted")
}

func TestTelegramNotifyMasksTokenInSendError(t *testing.T) {
	token := "12345:ABCDEF"
	notifier, err := newTelegramNotifier(
		"ops",
		token,
		"-1001234567890",
		TelegramOptions{
			Message: "{{.message}}",
		},
	)
	require.NoError(t, err, "create notifier")

	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("host unreachable")
		}),
	}

	_, err = notifier.Notify(
		context.Background(),
		Message{
			Payload: map[string]any{"message": "test"},
		},
	)
	require.Error(t, err, "notify must fail")
	assert.NotContains(t, err.Error(), token, "token must not leak to error")
	assert.Contains(t, err.Error(), "/bot[REDACTED]/sendMessage", "telegram bot path must be redacted")
}

func TestTelegramNotifyRetriesUntilSuccess(t *testing.T) {
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		TelegramOptions{
			Message: "{{.message}}",
			Retries: 3,
		},
	)
	require.NoError(t, err, "create notifier")

	attempts := 0
	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("temporary transport error")
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":42}}`)),
			}, nil
		}),
	}

	_, err = notifier.Notify(
		context.Background(),
		Message{
			Payload: map[string]any{"message": "test"},
		},
	)
	require.NoError(t, err, "notify must succeed after retries")
	assert.Equal(t, 3, attempts, "expected notify attempts")
}

func TestTelegramNotifyStopsAfterConfiguredRetries(t *testing.T) {
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		TelegramOptions{
			Message: "{{.message}}",
			Retries: 3,
		},
	)
	require.NoError(t, err, "create notifier")

	attempts := 0
	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("temporary transport error")
		}),
	}

	_, err = notifier.Notify(
		context.Background(),
		Message{
			Payload: map[string]any{"message": "test"},
		},
	)
	require.Error(t, err, "notify must fail")
	assert.Equal(t, 3, attempts, "expected notify attempts")
	assert.Contains(t, err.Error(), "after 3 attempts", "unexpected error")
}

func TestTelegramNotifyUsesDefaultRetries(t *testing.T) {
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		TelegramOptions{
			Message: "{{.message}}",
		},
	)
	require.NoError(t, err, "create notifier")

	attempts := 0
	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("temporary transport error")
		}),
	}

	_, err = notifier.Notify(
		context.Background(),
		Message{
			Payload: map[string]any{"message": "test"},
		},
	)
	require.Error(t, err, "notify must fail")
	assert.Equal(t, defaultTelegramRetries, attempts, "expected default notify attempts")
}

func TestTelegramNotifyReturnsRuntimeTemplateError(t *testing.T) {
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		TelegramOptions{Message: "{{ .Name }}"},
	)
	require.NoError(t, err, "create notifier")

	_, err = notifier.Notify(context.Background(), Message{Payload: map[string]any{"message": "test"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `map has no entry for key "Name"`)
}

func TestTelegramLifecycleOperations(t *testing.T) {
	tests := []struct {
		name          string
		message       Message
		firstStatus   int
		firstResponse string
		wantPaths     []string
		wantAction    Action
		wantID        int64
		wantErr       bool
	}{
		{name: "send", message: Message{Payload: map[string]string{"Title": "alert"}}, wantPaths: []string{"/sendMessage"}, wantAction: ActionSend, wantID: 71},
		{name: "reply", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionReply, TargetMessageID: 41}, wantPaths: []string{"/sendMessage"}, wantAction: ActionReply, wantID: 71},
		{name: "edit", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionEdit, TargetMessageID: 41}, wantPaths: []string{"/editMessageText"}, wantAction: ActionEdit, wantID: 41},
		{name: "missing original", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionReply}, wantPaths: []string{"/sendMessage"}, wantAction: ActionSend, wantID: 71},
		{name: "invalid reply reference", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionReply, TargetMessageID: 41}, firstStatus: 400, firstResponse: `{"ok":false,"description":"Bad Request: message to reply not found"}`, wantPaths: []string{"/sendMessage", "/sendMessage"}, wantAction: ActionSend, wantID: 71},
		{name: "invalid edit reference", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionEdit, TargetMessageID: 41}, firstStatus: 400, firstResponse: `{"ok":false,"description":"Bad Request: message to edit not found"}`, wantPaths: []string{"/editMessageText", "/sendMessage"}, wantAction: ActionSend, wantID: 71},
		{name: "temporary error", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionEdit, TargetMessageID: 41}, firstStatus: 503, firstResponse: `{"ok":false,"description":"unavailable"}`, wantPaths: []string{"/editMessageText"}, wantErr: true},
		{name: "other permanent error", message: Message{Payload: map[string]string{"Title": "alert"}, Action: ActionReply, TargetMessageID: 41}, firstStatus: 400, firstResponse: `{"ok":false,"description":"Bad Request: chat not found"}`, wantPaths: []string{"/sendMessage"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, strings.TrimPrefix(r.URL.Path, "/bot123:token"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies = append(bodies, body)
				if len(paths) == 1 && tt.firstStatus != 0 {
					w.WriteHeader(tt.firstStatus)
					_, _ = w.Write([]byte(tt.firstResponse))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":71}}`))
			}))
			defer server.Close()
			notifier, err := newTelegramNotifier("test", "123:token", "-100", TelegramOptions{
				Message: "{{ .Title }}", Retries: 1, APIBaseURL: server.URL, ChatThreadID: 7,
			})
			require.NoError(t, err)
			receipt, err := notifier.Notify(t.Context(), tt.message)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, receipt.MessageID)
				assert.Equal(t, tt.wantAction, receipt.Action)
			}
			assert.Equal(t, tt.wantPaths, paths)
			if tt.message.Action == ActionReply && tt.message.TargetMessageID != 0 {
				params, ok := bodies[0]["reply_parameters"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, float64(41), params["message_id"])
			}
			if tt.message.Action == ActionEdit && tt.message.TargetMessageID != 0 {
				assert.Equal(t, float64(41), bodies[0]["message_id"])
			}
		})
	}
}

func TestTelegramUsesSourceDefaultTemplate(t *testing.T) {
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		TelegramOptions{DefaultMessage: "{{ .message }}"},
	)
	require.NoError(t, err, "create notifier")

	actual, err := notifier.renderMessage(Message{Payload: map[string]any{"message": "source default"}})
	require.NoError(t, err)
	assert.Equal(t, "source default", actual)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
