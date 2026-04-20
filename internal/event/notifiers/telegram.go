package notifiers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/avast/retry-go/v5"
	"golang.org/x/net/proxy"
)

const defaultTelegramMessageTemplate = `deploy {{.status}}
stack_name: {{.stack_name}}
service: {{.service}}
image.full_name: {{.image.full_name}}
image.version: {{.image.version}}{{if .commit}}
commit: {{.commit}}{{end}}{{if .error}}
error: {{.error}}{{end}}`

var telegramBotSendMessagePathPattern = regexp.MustCompile(`/bot[^/\s]+/sendMessage`)

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

func NewTelegramNotifier(name, token, chatID string, options TelegramOptions) (*TelegramNotifier, error) {
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

	err = retry.New(
		retry.Attempts(n.retries),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	).Do(func() error {
		return n.sendRequest(ctx, body)
	})
	if err != nil {
		return fmt.Errorf("send request after %d attempts: %w", n.retries, err)
	}

	return nil
}

func (n *TelegramNotifier) sendRequest(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendMessage", n.apiBaseURL, n.token),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	//nolint:gosec // Telegram endpoint is configured by operator and required for outbound notifications.
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %s", maskTelegramSendError(err, n.token))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	return fmt.Errorf("unexpected status: %s, response: %s", resp.Status, string(respBody))
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
		Transport: transport,
	}, nil
}
