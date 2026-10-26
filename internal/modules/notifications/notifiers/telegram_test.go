package notifiers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avast/retry-go/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"go.uber.org/mock/gomock"
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
		&dispatcher.NopDispatcher{},
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
	attempts := 0
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	firstFailure := publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.MessengerUnavailable{})).
		DoAndReturn(func(context.Context, events.Event) error {
			assert.Equal(t, 1, attempts, "health failure must be published by the first request attempt")
			return nil
		})
	secondFailure := publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.MessengerUnavailable{})).Return(nil)
	reachable := publisher.EXPECT().Publish(gomock.Any(), &events.MessengerAvailable{ResourceID: "telegram"}).Return(nil)
	delivered := publisher.EXPECT().Publish(gomock.Any(), &events.MessengerDeliverySucceeded{ResourceID: "telegram:ops"}).Return(nil)
	gomock.InOrder(firstFailure, secondFailure, reachable, delivered)
	notifier, err := newTelegramNotifier(
		"ops",
		"12345:ABCDEF",
		"-1001234567890",
		publisher,
		TelegramOptions{
			Message: "{{.message}}",
			Retries: 3,
		},
	)
	require.NoError(t, err, "create notifier")
	disableTelegramRetryDelay(notifier)

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
		&dispatcher.NopDispatcher{},
		TelegramOptions{
			Message: "{{.message}}",
			Retries: 3,
		},
	)
	require.NoError(t, err, "create notifier")
	disableTelegramRetryDelay(notifier)

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
		&dispatcher.NopDispatcher{},
		TelegramOptions{
			Message: "{{.message}}",
		},
	)
	require.NoError(t, err, "create notifier")
	disableTelegramRetryDelay(notifier)

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
		&dispatcher.NopDispatcher{},
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
			notifier, err := newTelegramNotifier("test", "123:token", "-100", &dispatcher.NopDispatcher{}, TelegramOptions{
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
		&dispatcher.NopDispatcher{},
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

func disableTelegramRetryDelay(notifier *TelegramNotifier) {
	notifier.retryOptions = []retry.Option{retry.Delay(0), retry.MaxJitter(0)}
}

func TestTelegramStructuredDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"token=do-not-log","parameters":{"retry_after":17}}`))
	}))
	defer server.Close()
	notifier, err := newTelegramNotifier("ops", "123:token", "-100", &dispatcher.NopDispatcher{}, TelegramOptions{Message: "{{ .Title }}", Retries: 1, APIBaseURL: server.URL})
	require.NoError(t, err)
	_, err = notifier.Notify(t.Context(), Message{Payload: map[string]string{"Title": "test"}})
	require.Error(t, err)
	var apiErr *telegramAPIError
	require.ErrorAs(t, err, &apiErr)
	fields := apiErr.SafeLogFields()
	assert.Contains(t, fields, slog.String("failure_kind", "rate_limited"))
	assert.Contains(t, fields, slog.Int("http_status", 429))
	assert.Contains(t, fields, slog.Int("retry_after", 17))
	assert.NotContains(t, err.Error(), "do-not-log")
}

func TestTelegramPublishesMessengerHealthObservations(t *testing.T) {
	tests := []struct {
		name          string
		transportErr  error
		status        int
		response      string
		wantEvents    []events.Event
		wantNotifyErr bool
	}{
		{
			name: "dns failure", transportErr: &net.DNSError{Err: "no such host"}, wantNotifyErr: true,
			wantEvents: []events.Event{&events.MessengerUnavailable{
				ResourceID: "telegram", ErrorCode: "dns_error", Description: "Messenger DNS lookup failed",
			}},
		},
		{
			name: "api unavailable", status: http.StatusServiceUnavailable,
			response: `{"ok":false,"error_code":503,"description":"unavailable"}`, wantNotifyErr: true,
			wantEvents: []events.Event{&events.MessengerUnavailable{
				ResourceID: "telegram", ErrorCode: "server_error",
				Description: "Messenger API is temporarily unavailable",
			}},
		},
		{
			name: "invalid token", status: http.StatusUnauthorized,
			response: `{"ok":false,"error_code":401,"description":"Unauthorized"}`, wantNotifyErr: true,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerMisconfigured{ResourceID: "telegram:production", ErrorCode: "invalid_credentials",
					Description: "Messenger credentials were rejected"},
			},
		},
		{
			name: "chat missing", status: http.StatusBadRequest,
			response: `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, wantNotifyErr: true,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerMisconfigured{ResourceID: "telegram:production", ErrorCode: "chat_not_found",
					Description: "Messenger chat was not found"},
			},
		},
		{
			name: "thread missing", status: http.StatusBadRequest,
			response: `{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`, wantNotifyErr: true,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerMisconfigured{ResourceID: "telegram:production", ErrorCode: "thread_not_found",
					Description: "Messenger message thread was not found"},
			},
		},
		{
			name: "bot removed", status: http.StatusForbidden,
			response:      `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the supergroup chat"}`,
			wantNotifyErr: true,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerMisconfigured{ResourceID: "telegram:production", ErrorCode: "bot_removed",
					Description: "Messenger bot is no longer available in the chat"},
			},
		},
		{
			name: "send permission missing", status: http.StatusForbidden,
			response:      `{"ok":false,"error_code":403,"description":"Forbidden: not enough rights to send text messages"}`,
			wantNotifyErr: true,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerMisconfigured{ResourceID: "telegram:production", ErrorCode: "permission_denied",
					Description: "Messenger bot cannot send messages to the chat"},
			},
		},
		{
			name: "rate limited is reachable", status: http.StatusTooManyRequests,
			response: `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, wantNotifyErr: true,
			wantEvents: []events.Event{&events.MessengerAvailable{ResourceID: "telegram"}},
		},
		{
			name: "delivery succeeded", status: http.StatusOK,
			response: `{"ok":true,"result":{"message_id":42}}`,
			wantEvents: []events.Event{
				&events.MessengerAvailable{ResourceID: "telegram"},
				&events.MessengerDeliverySucceeded{ResourceID: "telegram:production"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
			calls := make([]any, 0, len(test.wantEvents))
			for _, event := range test.wantEvents {
				calls = append(calls, publisher.EXPECT().Publish(gomock.Any(), gomock.Eq(event)).Return(nil))
			}
			gomock.InOrder(calls...)
			notifier, err := newTelegramNotifier("production", "123:token", "-100", publisher,
				TelegramOptions{Message: "{{ .Title }}", Retries: 1})
			require.NoError(t, err)
			notifier.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				return &http.Response{
					StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.response)),
				}, nil
			})}
			_, err = notifier.Notify(t.Context(), Message{Payload: map[string]string{"Title": "test"}})
			if test.wantNotifyErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTelegramHealthPublicationFailureDoesNotFailDelivery(t *testing.T) {
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Times(2).Return(assert.AnError)
	notifier, err := newTelegramNotifier("production", "123:token", "-100", publisher,
		TelegramOptions{Message: "{{ .Title }}", Retries: 1})
	require.NoError(t, err)
	notifier.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":42}}`))}, nil
	})}
	_, err = notifier.Notify(t.Context(), Message{Payload: map[string]string{"Title": "test"}})
	require.NoError(t, err)
}

func TestTelegramHealthResourceIdentityUsesChannelName(t *testing.T) {
	assert.Equal(t, "telegram", telegramMessengerResourceID())
	assert.Equal(t, "telegram:production", telegramChannelResourceID("production"))
	assert.Equal(t, "telegram:staging", telegramChannelResourceID("staging"))
	first := &TelegramNotifier{name: "production", chatID: "-100", chatThreadID: 1}
	changed := &TelegramNotifier{name: "production", chatID: "-200", chatThreadID: 99}
	assert.Equal(t, telegramChannelResourceID(first.name), telegramChannelResourceID(changed.name))
}
