package notifiers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/net/proxy"
)

const defaultTelegramMessageTemplate = `deploy {{.status}}
stack_name: {{.stack_name}}
service: {{.service}}
image.full_name: {{.image.full_name}}
image.version: {{.image.version}}{{if .commit}}
commit: {{.commit}}{{end}}{{if .error}}
error: {{.error}}{{end}}`

var telegramBotSendMessagePathPattern = regexp.MustCompile(`/bot[^/\s]+/(sendMessage|editMessageText)`)

type TelegramOptions struct {
	// ChatThreadID is a thread/topic identifier in Telegram chat.
	ChatThreadID int64
	// Message is a Go template used for notification body.
	Message string
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
	messageTmpl  *template.Template
	retries      uint
	client       *http.Client
}

const defaultTelegramRetries = 3

func NewTelegramNotifier(name, token, chatID string, options TelegramOptions) (Notifier, error) {
	tgNotifier, err := newTelegramNotifier(name, token, chatID, options)
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

func newTelegramNotifier(name, token, chatID string, options TelegramOptions) (*TelegramNotifier, error) {
	templateText := strings.TrimSpace(options.Message)
	if templateText == "" {
		templateText = defaultTelegramMessageTemplate
	}

	tmpl, err := template.New("telegram-message").Parse(templateText)
	if err != nil {
		return nil, fmt.Errorf("parse telegram message template: %w", err)
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
		retries:      retries,
		client:       client,
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

// Notify renders the message and sends it, edits the message in Message.EditMessageID
// or replies to the message in Message.ReplyToID.
func (n *TelegramNotifier) Notify(ctx context.Context, event Message) (delivery.Receipt, error) {
	if event.EditMessageID != "" && event.ReplyToID != "" {
		return delivery.Receipt{}, errors.New("edit message id and reply to id are mutually exclusive")
	}

	message, err := n.renderMessage(event)
	if err != nil {
		return delivery.Receipt{}, err
	}

	payload := map[string]any{
		"chat_id": n.chatID,
		"text":    message,
	}
	method := "sendMessage"

	switch {
	case event.EditMessageID != "":
		messageID, parseErr := strconv.ParseInt(event.EditMessageID, 10, 64)
		if parseErr != nil {
			return delivery.Receipt{}, fmt.Errorf("parse edit message id: %w", parseErr)
		}
		method = "editMessageText"
		payload["message_id"] = messageID
	default:
		if n.chatThreadID > 0 {
			payload["message_thread_id"] = n.chatThreadID
		}
		if event.ReplyToID != "" {
			replyToID, parseErr := strconv.ParseInt(event.ReplyToID, 10, 64)
			if parseErr != nil {
				return delivery.Receipt{}, fmt.Errorf("parse reply to id: %w", parseErr)
			}
			payload["reply_parameters"] = map[string]any{
				"message_id":                  replyToID,
				"allow_sending_without_reply": true,
			}
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return delivery.Receipt{}, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := n.call(ctx, method, body)
	if err != nil {
		if event.EditMessageID != "" {
			return n.editResult(err, event.EditMessageID)
		}
		return delivery.Receipt{}, err
	}

	if event.EditMessageID != "" {
		return delivery.Receipt{MessageID: event.EditMessageID}, nil
	}

	var parsed struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if json.Unmarshal(resp, &parsed) != nil || parsed.Result.MessageID == 0 {
		// The message is delivered; without message_id it just cannot be edited or replied to later.
		return delivery.Receipt{}, nil
	}

	return delivery.Receipt{MessageID: strconv.FormatInt(parsed.Result.MessageID, 10)}, nil
}

// editResult classifies an editMessageText failure.
func (n *TelegramNotifier) editResult(err error, messageID string) (delivery.Receipt, error) {
	var apiErr *telegramAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return delivery.Receipt{}, err
	}

	body := strings.ToLower(apiErr.Body)
	switch {
	case strings.Contains(body, "message is not modified"):
		// Repeated processing with the same text: the message is already up to date.
		return delivery.Receipt{MessageID: messageID}, nil
	case strings.Contains(body, "message to edit not found"),
		strings.Contains(body, "message can't be edited"),
		strings.Contains(body, "message_id_invalid"):
		return delivery.Receipt{}, fmt.Errorf("%w: %w", ErrMessageUnavailable, err)
	default:
		return delivery.Receipt{}, err
	}
}

// telegramAPIError is a non-successful Telegram Bot API response.
type telegramAPIError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Status is the HTTP status line.
	Status string
	// Body is the response body.
	Body string
}

func (e *telegramAPIError) Error() string {
	return fmt.Sprintf("unexpected status: %s, response: %s", e.Status, e.Body)
}

// call performs a Bot API method with retries and returns the response body.
func (n *TelegramNotifier) call(ctx context.Context, method string, body []byte) ([]byte, error) {
	resp, err := retry.NewWithData[[]byte](
		retry.Attempts(n.retries),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	).Do(func() ([]byte, error) {
		return n.sendRequest(ctx, method, body)
	})
	if err != nil {
		return nil, fmt.Errorf("%s after %d attempts: %w", method, n.retries, err)
	}

	return resp, nil
}

func (n *TelegramNotifier) sendRequest(ctx context.Context, method string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", n.apiBaseURL, n.token, method),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	//nolint:gosec // Telegram endpoint is configured by operator and required for outbound notifications.
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %s", maskTelegramSendError(err, n.token))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return respBody, nil
	}

	return nil, &telegramAPIError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(respBody)}
}

func (n *TelegramNotifier) renderMessage(event Message) (string, error) {
	data := map[string]any{
		"event": event.Payload,
	}
	if fields, ok := event.Payload.(Fields); ok {
		for key, value := range fields {
			data[key] = value
		}
	}

	var out bytes.Buffer
	if err := n.messageTmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render telegram message template: %w", err)
	}

	return out.String(), nil
}

func maskTelegramSendError(err error, token string) string {
	message := err.Error()
	message = strings.ReplaceAll(message, token, "[REDACTED]")

	return telegramBotSendMessagePathPattern.ReplaceAllString(message, "/bot[REDACTED]/$1")
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
