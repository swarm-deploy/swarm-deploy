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

// NewTelegramTransport builds a Telegram channel that can send with receipt and edit messages.
func NewTelegramTransport(name, token, chatID string, options TelegramOptions) (*TelegramNotifier, error) {
	return newTelegramNotifier(name, token, chatID, options)
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

func (n *TelegramNotifier) Notify(ctx context.Context, event Message) error {
	message, err := n.renderMessage(event)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"chat_id": n.chatID,
		"text":    message,
	}
	if n.chatThreadID > 0 {
		payload["message_thread_id"] = n.chatThreadID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	if _, err = n.call(ctx, "sendMessage", body); err != nil {
		return err
	}

	return nil
}

// ID returns the channel identifier used for delivery correlation.
func (n *TelegramNotifier) ID() string {
	return n.Name()
}

// Send sends text and returns a receipt containing the Telegram message_id.
func (n *TelegramNotifier) Send(ctx context.Context, text string) (delivery.Receipt, error) {
	payload := map[string]any{
		"chat_id": n.chatID,
		"text":    text,
	}
	if n.chatThreadID > 0 {
		payload["message_thread_id"] = n.chatThreadID
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := n.call(ctx, "sendMessage", body)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err = json.Unmarshal(resp, &parsed); err != nil || parsed.Result.MessageID == 0 {
		// The message is delivered; without message_id it just cannot be edited later.
		return nil, nil //nolint:nilerr // see above
	}

	return delivery.Receipt{telegramReceiptMessageID: strconv.FormatInt(parsed.Result.MessageID, 10)}, nil
}

// Edit replaces text of a message previously sent by Send.
func (n *TelegramNotifier) Edit(ctx context.Context, receipt delivery.Receipt, text string) error {
	messageID, err := strconv.ParseInt(receipt[telegramReceiptMessageID], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad message_id", delivery.ErrReceiptInvalid)
	}

	body, err := json.Marshal(map[string]any{
		"chat_id":    n.chatID,
		"message_id": messageID,
		"text":       text,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	_, err = n.call(ctx, "editMessageText", body)
	var apiErr *telegramAPIError
	if errors.As(err, &apiErr) {
		switch {
		case strings.Contains(apiErr.Body, "message is not modified"):
			// Repeated edit with the same text.
			return nil
		case apiErr.StatusCode == http.StatusBadRequest || apiErr.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%w: %w", delivery.ErrReceiptInvalid, err)
		}
	}

	return err
}

const telegramReceiptMessageID = "message_id"

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
