package notifiers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/avast/retry-go/v5"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/net/proxy"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

var telegramBotSendMessagePathPattern = regexp.MustCompile(`/bot[^/\s]+/(sendMessage|editMessageText)`)

// ErrMessageReferenceInvalid means Telegram permanently rejected a reply/edit target.
var ErrMessageReferenceInvalid = errors.New("telegram message reference is invalid")

type TelegramOptions struct {
	// ChatThreadID is a thread/topic identifier in Telegram chat.
	ChatThreadID int64
	// Message is a Go template used for notification body.
	Message string
	// DefaultMessage is the source-owned template used when Message is empty.
	DefaultMessage string
	// ParseMode selects Telegram message entity parsing.
	ParseMode string
	// APIBaseURL is a base URL for Telegram Bot API.
	APIBaseURL string
	// Retries is a number of send attempts for Telegram notification.
	Retries uint
	// SOCKS5Address is an optional SOCKS5 proxy address in host:port format.
	SOCKS5Address string
}

type TelegramNotifier struct {
	name         string
	token        string
	chatID       string
	chatThreadID int64
	apiBaseURL   string
	messageTmpl  *notificationtemplate.Renderer
	parseMode    string
	retries      uint
	retryOptions []retry.Option
	client       *http.Client
	publisher    interface {
		Publish(context.Context, events.Event) error
	}
}

const defaultTelegramRetries = 3

const (
	telegramSendEndpoint  = "sendMessage"
	telegramEditEndpoint  = "editMessageText"
	telegramResponseLimit = 64 * 1024
)

func NewTelegramNotifier(name, token, chatID string, publisher interface {
	Publish(context.Context, events.Event) error
}, options TelegramOptions) (Notifier, error) {
	tgNotifier, err := newTelegramNotifier(name, token, chatID, publisher, options)
	if err != nil {
		return nil, err
	}

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return tgNotifier, nil
	}

	return NewTraceableNotifier(tgNotifier, tp, []attribute.KeyValue{
		{
			Key:   "notifier.telegram.api_base_url",
			Value: attribute.StringValue(tgNotifier.apiBaseURL),
		},
	}), nil
}

func newTelegramNotifier(name, token, chatID string, publisher interface {
	Publish(context.Context, events.Event) error
}, options TelegramOptions) (*TelegramNotifier, error) {
	templateText := strings.TrimSpace(options.Message)
	if templateText == "" {
		templateText = strings.TrimSpace(options.DefaultMessage)
	}
	if templateText == "" {
		return nil, fmt.Errorf("message template is required")
	}

	tmpl, err := notificationtemplate.New(templateText)
	if err != nil {
		return nil, fmt.Errorf("build telegram message template: %w", err)
	}

	apiBaseURL := options.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = "https://api.telegram.org"
	}

	retries := options.Retries
	if retries <= 0 {
		retries = defaultTelegramRetries
	}

	client, err := buildTelegramHTTPClient(options.SOCKS5Address)
	if err != nil {
		return nil, fmt.Errorf("build http client: %w", err)
	}

	return &TelegramNotifier{
		name:         name,
		token:        token,
		chatID:       chatID,
		chatThreadID: options.ChatThreadID,
		apiBaseURL:   strings.TrimRight(apiBaseURL, "/"),
		messageTmpl:  tmpl,
		parseMode:    options.ParseMode,
		retries:      retries,
		client:       client,
		publisher:    publisher,
	}, nil
}

func (n *TelegramNotifier) Name() string {
	if n.name != "" {
		return "notifier-telegram-" + n.name
	}
	return "notifier-telegram"
}

func (*TelegramNotifier) Kind() string {
	return "telegram"
}

func (n *TelegramNotifier) Notify(ctx context.Context, event Message) (Receipt, error) {
	message, err := n.renderMessage(event)
	if err != nil {
		return Receipt{}, err
	}
	action := event.Action
	if action == "" || event.TargetMessageID == 0 {
		action = ActionSend
	}
	receipt, err := n.notifyAction(ctx, message, action, event.TargetMessageID)
	if errors.Is(err, ErrMessageReferenceInvalid) && action != ActionSend {
		return n.notifyAction(ctx, message, ActionSend, 0)
	}
	return receipt, err
}

func (n *TelegramNotifier) notifyAction(
	ctx context.Context, message string, action Action, targetID int64,
) (Receipt, error) {
	payload := map[string]any{
		"chat_id": n.chatID,
		"text":    message,
	}
	if n.parseMode != "" {
		payload["parse_mode"] = n.parseMode
	}
	endpoint := telegramSendEndpoint
	switch action {
	case ActionSend:
	case ActionEdit:
		endpoint = telegramEditEndpoint
		payload["message_id"] = targetID
	case ActionReply:
		payload["reply_parameters"] = map[string]any{"message_id": targetID}
	}
	if n.chatThreadID > 0 && action != ActionEdit {
		payload["message_thread_id"] = n.chatThreadID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, fmt.Errorf("marshal request: %w", err)
	}
	var receipt Receipt
	retryOptions := []retry.Option{
		retry.Attempts(n.retries),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	}
	retryOptions = append(retryOptions, n.retryOptions...)
	err = retry.New(retryOptions...).Do(func() error {
		var sendErr error
		receipt, sendErr = n.sendRequest(ctx, endpoint, body, targetID)
		if sendErr != nil && !errors.Is(sendErr, ErrMessageReferenceInvalid) {
			var apiErr *telegramAPIError
			if errors.As(sendErr, &apiErr) && apiErr.status >= http.StatusBadRequest &&
				apiErr.status < http.StatusInternalServerError && apiErr.status != http.StatusTooManyRequests {
				return retry.Unrecoverable(sendErr)
			}
		}
		if errors.Is(sendErr, ErrMessageReferenceInvalid) {
			return retry.Unrecoverable(sendErr)
		}
		return sendErr
	})
	if err != nil {
		return Receipt{}, fmt.Errorf("telegram %s after %d attempts: %w", endpoint, n.retries, err)
	}
	return receipt, nil
}

type telegramTransportError struct{ kind string }

func (e *telegramTransportError) Error() string { return "telegram transport " + e.kind }

func (e *telegramTransportError) SafeLogFields() []slog.Attr {
	return []slog.Attr{
		slog.String("failure_source", "telegram"),
		slog.String("failure_kind", e.kind),
	}
}

type telegramAPIError struct {
	status     int
	code       int
	kind       string
	retryAfter int
}

func (e *telegramAPIError) SafeLogFields() []slog.Attr {
	fields := []slog.Attr{
		slog.String("failure_source", "telegram"),
		slog.String("failure_kind", e.kind),
		slog.Int("http_status", e.status),
	}
	if e.code != 0 {
		fields = append(fields, slog.Int("telegram_error_code", e.code))
	}
	if e.retryAfter > 0 {
		fields = append(fields, slog.Int("retry_after", e.retryAfter))
	}
	return fields
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("telegram API status %d", e.status)
}

func (n *TelegramNotifier) sendRequest(
	ctx context.Context, endpoint string, body []byte, targetID int64,
) (Receipt, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", n.apiBaseURL, n.token, endpoint),
		bytes.NewReader(body),
	)
	if err != nil {
		return Receipt{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// #nosec G704 -- Telegram API base URL is explicitly configured by the operator.
	resp, err := n.client.Do(req)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			n.publishHealthEvent(ctx, &events.MessengerUnavailable{
				ResourceID: telegramMessengerResourceID(), ErrorCode: faults.TransportErrorKind(err),
				Description: telegramTransportDescription(err),
			})
		}
		return Receipt{}, fmt.Errorf("send request: %w", errors.Join(
			&telegramTransportError{kind: faults.TransportErrorKind(err)},
			errors.New(maskTelegramSendError(err, n.token)),
		))
	}
	defer resp.Body.Close()
	n.publishReachability(ctx, resp.StatusCode)
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, telegramResponseLimit))
	if err != nil {
		return Receipt{}, fmt.Errorf("read response body: %w", err)
	}
	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		ErrorCode   int    `json:"error_code"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(respBody, &answer); err != nil {
		return Receipt{}, fmt.Errorf("decode telegram response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || !answer.OK {
		if endpoint == telegramEditEndpoint && resp.StatusCode == http.StatusBadRequest &&
			strings.Contains(strings.ToLower(answer.Description), "message is not modified") {
			receipt := Receipt{MessageID: targetID, Action: ActionEdit}
			n.publishHealthEvent(ctx, &events.MessengerDeliverySucceeded{
				ResourceID: telegramChannelResourceID(n.name),
			})
			return receipt, nil
		}
		if targetID != 0 && resp.StatusCode == http.StatusBadRequest &&
			invalidTelegramReference(endpoint, answer.Description) {
			return Receipt{}, ErrMessageReferenceInvalid
		}
		apiErr := &telegramAPIError{
			status:     resp.StatusCode,
			code:       answer.ErrorCode,
			kind:       faults.HTTPCodeKind(resp.StatusCode),
			retryAfter: answer.Parameters.RetryAfter,
		}
		n.publishMisconfiguration(ctx, resp.StatusCode, answer.Description)
		return Receipt{}, apiErr
	}
	if endpoint == telegramEditEndpoint && string(answer.Result) == "true" {
		receipt := Receipt{MessageID: targetID, Action: ActionEdit}
		n.publishHealthEvent(ctx, &events.MessengerDeliverySucceeded{
			ResourceID: telegramChannelResourceID(n.name),
		})
		return receipt, nil
	}
	var result struct {
		MessageID int64 `json:"message_id"`
	}
	if err = json.Unmarshal(answer.Result, &result); err != nil {
		return Receipt{}, fmt.Errorf("decode telegram result: %w", err)
	}
	if endpoint == telegramEditEndpoint {
		result.MessageID = targetID
	}
	action := ActionSend
	if endpoint == telegramEditEndpoint {
		action = ActionEdit
	} else if targetID != 0 {
		action = ActionReply
	}
	receipt := Receipt{MessageID: result.MessageID, Action: action}
	n.publishHealthEvent(ctx, &events.MessengerDeliverySucceeded{
		ResourceID: telegramChannelResourceID(n.name),
	})
	return receipt, nil
}

func (n *TelegramNotifier) publishReachability(ctx context.Context, status int) {
	if status >= http.StatusInternalServerError {
		n.publishHealthEvent(ctx, &events.MessengerUnavailable{
			ResourceID: telegramMessengerResourceID(), ErrorCode: faults.HTTPCodeKind(status),
			Description: "Messenger API is temporarily unavailable",
		})
		return
	}
	n.publishHealthEvent(ctx, &events.MessengerAvailable{ResourceID: telegramMessengerResourceID()})
}

func (n *TelegramNotifier) publishMisconfiguration(ctx context.Context, status int, apiDescription string) {
	code, description, misconfigured := classifyTelegramConfigurationError(status, apiDescription)
	if !misconfigured {
		return
	}
	n.publishHealthEvent(ctx, &events.MessengerMisconfigured{
		ResourceID: telegramChannelResourceID(n.name), ErrorCode: code, Description: description,
	})
}

func (n *TelegramNotifier) publishHealthEvent(ctx context.Context, event events.Event) {
	if err := n.publisher.Publish(ctx, event); err != nil {
		slog.ErrorContext(ctx, "publish messenger health event failed",
			"event_type", event.Type().String(), "messenger", "telegram", "channel", n.name,
			"error_type", fmt.Sprintf("%T", err),
		)
	}
}

func telegramMessengerResourceID() string { return "telegram" }

func telegramChannelResourceID(channelName string) string { return "telegram:" + channelName }

func telegramTransportDescription(err error) string {
	switch faults.TransportErrorKind(err) {
	case "dns_error":
		return "Messenger DNS lookup failed"
	case "timeout":
		return "Messenger connection timed out"
	default:
		return "Messenger connection failed"
	}
}

func classifyTelegramConfigurationError(status int, description string) (string, string, bool) {
	if status == http.StatusUnauthorized {
		return "invalid_credentials", "Messenger credentials were rejected", true
	}
	description = strings.ToLower(description)
	switch {
	case status == http.StatusBadRequest && strings.Contains(description, "message thread not found"):
		return "thread_not_found", "Messenger message thread was not found", true
	case status == http.StatusBadRequest && strings.Contains(description, "chat not found"):
		return "chat_not_found", "Messenger chat was not found", true
	case status == http.StatusForbidden && (strings.Contains(description, "kicked") ||
		strings.Contains(description, "not a member") || strings.Contains(description, "blocked")):
		return "bot_removed", "Messenger bot is no longer available in the chat", true
	case status == http.StatusForbidden,
		status == http.StatusBadRequest && (strings.Contains(description, "not enough rights") ||
			strings.Contains(description, "have no rights") || strings.Contains(description, "not permitted")):
		return "permission_denied", "Messenger bot cannot send messages to the chat", true
	default:
		return "", "", false
	}
}

func invalidTelegramReference(endpoint, description string) bool {
	description = strings.ToLower(description)
	if strings.Contains(description, "message_id_invalid") {
		return true
	}
	if endpoint == telegramEditEndpoint {
		return strings.Contains(description, "message to edit not found") ||
			strings.Contains(description, "message can't be edited") ||
			strings.Contains(description, "message can not be edited")
	}
	return strings.Contains(description, "message to reply not found") ||
		strings.Contains(description, "replied message not found")
}

func (n *TelegramNotifier) renderMessage(event Message) (string, error) {
	message, err := n.messageTmpl.Render(event.Payload)
	if err != nil {
		return "", fmt.Errorf("render telegram message template: %w", err)
	}
	return message, nil
}

func maskTelegramSendError(err error, token string) string {
	message := err.Error()
	message = strings.ReplaceAll(message, token, "[REDACTED]")

	return telegramBotSendMessagePathPattern.ReplaceAllString(message, "/bot[REDACTED]/sendMessage")
}

func buildTelegramHTTPClient(socks5Address string) (*http.Client, error) {
	if socks5Address == "" {
		return &http.Client{Timeout: defaultNotifyHTTPTimeout}, nil
	}

	dialer, err := proxy.SOCKS5("tcp", socks5Address, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("build socks5 dialer: %w", err)
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		IdleConnTimeout:       time.Minute,
		TLSHandshakeTimeout:   time.Minute,
		ExpectContinueTimeout: time.Minute,
	}

	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		transport.DialContext = contextDialer.DialContext
	} else {
		transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		}
	}

	return &http.Client{
		Timeout:   defaultNotifyHTTPTimeout,
		Transport: traceTransport(transport, "/sendMessage"),
	}, nil
}
