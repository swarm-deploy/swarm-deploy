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
	notifier, err := NewTelegramNotifier(
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
	notifier, err := NewTelegramNotifier(
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
	notifier, err := NewTelegramNotifier(
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
	notifier, err := NewTelegramNotifier(
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
