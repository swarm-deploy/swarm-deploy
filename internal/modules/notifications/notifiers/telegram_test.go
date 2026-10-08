package notifiers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
			Message: "{{.event.message}}",
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
			Message: "{{.event.message}}",
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
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
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
			Message: "{{.event.message}}",
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
			Message: "{{.event.message}}",
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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestTelegram(t *testing.T, status int, body string, options TelegramOptions, calls *[]map[string]any) *TelegramNotifier {
	t.Helper()
	options.Retries = 1
	notifier, err := newTelegramNotifier("ops", "12345:ABCDEF", "-100", options)
	require.NoError(t, err, "create notifier")
	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			payload, readErr := io.ReadAll(req.Body)
			require.NoError(t, readErr, "read request")
			decoded := map[string]any{}
			require.NoError(t, json.Unmarshal(payload, &decoded), "decode request")
			decoded["_method"] = req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			*calls = append(*calls, decoded)
			return &http.Response{
				StatusCode: status,
				Status:     http.StatusText(status),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
	return notifier
}

func TestTelegramNotifySendReturnsReceiptAndRendersEventTemplate(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true,"result":{"message_id":77}}`,
		TelegramOptions{Message: "msg: {{.event.message}}", ChatThreadID: 5}, &calls)

	receipt, err := notifier.Notify(context.Background(), Message{Payload: map[string]any{"message": "test"}})
	require.NoError(t, err, "notify")
	assert.Equal(t, "77", receipt.MessageID, "receipt carries message_id")
	require.Len(t, calls, 1, "one request")
	assert.Equal(t, "sendMessage", calls[0]["_method"], "method")
	assert.Equal(t, "msg: test", calls[0]["text"], "event template")
	assert.EqualValues(t, 5, calls[0]["message_thread_id"], "thread")
	assert.NotContains(t, calls[0], "reply_parameters", "plain send")
}

func TestTelegramNotifySendWithoutMessageIDReturnsEmptyReceipt(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true}`, TelegramOptions{}, &calls)

	receipt, err := notifier.Notify(context.Background(), Message{Payload: map[string]any{"message": "x"}})
	require.NoError(t, err, "notify")
	assert.Empty(t, receipt.MessageID, "no message_id")
}

func TestTelegramNotifyEdit(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		body            string
		wantErr         bool
		wantUnavailable bool
	}{
		{name: "success", status: http.StatusOK, body: `{"ok":true}`},
		{
			name: "not modified is success on repeated processing", status: http.StatusBadRequest,
			body: `{"description":"Bad Request: message is not modified"}`,
		},
		{
			name: "message not found", status: http.StatusBadRequest,
			body:    `{"description":"Bad Request: message to edit not found"}`,
			wantErr: true, wantUnavailable: true,
		},
		{
			name: "other bad request is not unavailable", status: http.StatusBadRequest,
			body: `{"description":"Bad Request: can't parse entities"}`, wantErr: true,
		},
		{
			name: "forbidden is not unavailable", status: http.StatusForbidden,
			body: `{"description":"Forbidden: bot was blocked by the user"}`, wantErr: true,
		},
		{name: "server error", status: http.StatusInternalServerError, body: `oops`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []map[string]any
			notifier := newTestTelegram(t, tt.status, tt.body, TelegramOptions{Message: "text"}, &calls)

			receipt, err := notifier.Notify(context.Background(), Message{EditMessageID: "5", Payload: map[string]any{}})
			assert.Equal(t, tt.wantErr, err != nil, "error")
			assert.Equal(t, tt.wantUnavailable, errors.Is(err, ErrMessageUnavailable), "unavailable")
			if err != nil {
				assert.NotContains(t, err.Error(), "12345:ABCDEF", "token must not leak")
			} else {
				assert.Equal(t, "5", receipt.MessageID, "receipt")
			}
			require.Len(t, calls, 1, "one request")
			assert.Equal(t, "editMessageText", calls[0]["_method"], "method")
			assert.EqualValues(t, 5, calls[0]["message_id"], "message id is numeric")
		})
	}
}

func TestTelegramNotifyReply(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true,"result":{"message_id":78}}`,
		TelegramOptions{Message: "text"}, &calls)

	receipt, err := notifier.Notify(context.Background(), Message{ReplyToID: "77", Payload: map[string]any{}})
	require.NoError(t, err, "notify")
	assert.Equal(t, "78", receipt.MessageID, "receipt")
	assert.Equal(t, "sendMessage", calls[0]["_method"], "method")
	assert.Equal(t, map[string]any{"message_id": float64(77), "allow_sending_without_reply": true},
		calls[0]["reply_parameters"], "reply parameters")
}

func TestTelegramNotifyRejectsEditAndReplyTogetherAndBadIDs(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true}`, TelegramOptions{Message: "text"}, &calls)

	for name, msg := range map[string]Message{
		"both":     {EditMessageID: "1", ReplyToID: "2"},
		"bad edit": {EditMessageID: "abc"},
		"bad then": {ReplyToID: "abc"},
	} {
		_, err := notifier.Notify(context.Background(), msg)
		require.Error(t, err, name)
	}
	assert.Empty(t, calls, "nothing is sent on invalid input")
}

func TestTelegramNotifyRendersAlertFields(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true}`,
		TelegramOptions{Message: "{{.alert.ID}}|{{.resolution}}|{{.status}}|{{.event.status}}"}, &calls)

	_, err := notifier.Notify(context.Background(), Message{Payload: Fields{
		"alert": struct{ ID string }{ID: "a1"}, "resolution": "fixed", "status": "resolved",
	}})
	require.NoError(t, err, "notify")
	assert.Equal(t, "a1|fixed|resolved|resolved", calls[0]["text"], "alert template fields")
}

func TestTelegramSendError(t *testing.T) {
	var calls []map[string]any
	notifier := newTestTelegram(t, http.StatusBadGateway, `bad gateway`, TelegramOptions{Message: "x"}, &calls)

	_, err := notifier.Notify(context.Background(), Message{})
	require.Error(t, err, "send must fail")
	assert.Contains(t, err.Error(), "bad gateway", "response is reported")
	assert.NotContains(t, err.Error(), "12345:ABCDEF", "token must not leak")
}

func TestCustomWebhookIgnoresMessageIDsInBody(t *testing.T) {
	body, err := json.Marshal(Message{EditMessageID: "1", ReplyToID: "2", Payload: "p"})
	require.NoError(t, err, "marshal")
	assert.JSONEq(t, `{"Payload":"p"}`, string(body), "webhook body is unchanged")
}
