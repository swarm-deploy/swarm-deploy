package notifiers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
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

	err = notifier.Notify(
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

	err = notifier.Notify(
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

	err = notifier.Notify(
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

	err = notifier.Notify(
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

func newTestTelegram(t *testing.T, status int, body string, calls *[]*http.Request) *TelegramNotifier {
	t.Helper()
	notifier, err := newTelegramNotifier("ops", "12345:ABCDEF", "-100", TelegramOptions{Retries: 1})
	require.NoError(t, err, "create notifier")
	notifier.client = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			*calls = append(*calls, req)
			return &http.Response{
				StatusCode: status,
				Status:     http.StatusText(status),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
	return notifier
}

func TestTelegramSendReturnsReceipt(t *testing.T) {
	var calls []*http.Request
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true,"result":{"message_id":77}}`, &calls)

	receipt, err := notifier.Send(context.Background(), "hello")
	require.NoError(t, err, "send")
	assert.Equal(t, delivery.Receipt{"message_id": "77"}, receipt, "receipt carries message_id")
	assert.True(t, strings.HasSuffix(calls[0].URL.Path, "/sendMessage"), "sendMessage called")
}

func TestTelegramSendWithoutMessageIDReturnsEmptyReceipt(t *testing.T) {
	var calls []*http.Request
	notifier := newTestTelegram(t, http.StatusOK, `{"ok":true}`, &calls)

	receipt, err := notifier.Send(context.Background(), "hello")
	require.NoError(t, err, "send")
	assert.Empty(t, receipt, "message cannot be edited without message_id")
}

func TestTelegramEdit(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		receipt     delivery.Receipt
		wantErr     bool
		wantInvalid bool
	}{
		{name: "success", status: http.StatusOK, body: `{"ok":true}`, receipt: delivery.Receipt{"message_id": "5"}},
		{
			name: "not modified is success on repeated processing", status: http.StatusBadRequest,
			body: `{"description":"Bad Request: message is not modified"}`, receipt: delivery.Receipt{"message_id": "5"},
		},
		{
			name: "message not found", status: http.StatusBadRequest,
			body: `{"description":"Bad Request: message to edit not found"}`, receipt: delivery.Receipt{"message_id": "5"},
			wantErr: true, wantInvalid: true,
		},
		{
			name: "server error is not an invalid receipt", status: http.StatusInternalServerError,
			body: `oops`, receipt: delivery.Receipt{"message_id": "5"}, wantErr: true,
		},
		{name: "missing message_id", receipt: delivery.Receipt{}, wantErr: true, wantInvalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []*http.Request
			notifier := newTestTelegram(t, tt.status, tt.body, &calls)

			err := notifier.Edit(context.Background(), tt.receipt, "text")
			assert.Equal(t, tt.wantErr, err != nil, "error")
			assert.Equal(t, tt.wantInvalid, errors.Is(err, delivery.ErrReceiptInvalid), "invalid receipt")
			if err != nil {
				assert.NotContains(t, err.Error(), "12345:ABCDEF", "token must not leak")
			}
		})
	}
}

func TestTelegramSendError(t *testing.T) {
	var calls []*http.Request
	notifier := newTestTelegram(t, http.StatusBadGateway, `bad gateway`, &calls)

	_, err := notifier.Send(context.Background(), "hello")
	require.Error(t, err, "send must fail")
	assert.Contains(t, err.Error(), "bad gateway", "response is reported")
}
