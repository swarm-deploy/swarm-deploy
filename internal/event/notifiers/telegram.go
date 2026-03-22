package notifiers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
)

const defaultTelegramMessageTemplate = `deploy {{.status}}
stack_name: {{.stack_name}}
service: {{.service}}
image.full_name: {{.image.full_name}}
image.version: {{.image.version}}{{if .commit}}
commit: {{.commit}}{{end}}{{if .error}}
error: {{.error}}{{end}}`

type TelegramOptions struct {
	ChatThreadID int64
	Message      string
	APIBaseURL   string
}

type TelegramNotifier struct {
	name         string
	token        string
	chatID       string
	chatThreadID int64
	apiBaseURL   string
	messageTmpl  *template.Template
	client       *http.Client
}

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

	return &TelegramNotifier{
		name:         name,
		token:        token,
		chatID:       chatID,
		chatThreadID: options.ChatThreadID,
		apiBaseURL:   strings.TrimRight(apiBaseURL, "/"),
		messageTmpl:  tmpl,
		client:       &http.Client{Timeout: defaultNotifyHTTPTimeout},
	}, nil
}

func (n *TelegramNotifier) Name() string {
	if n.name != "" {
		return n.name
	}
	return "telegram"
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
		return fmt.Errorf("send request: %w", err)
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
