package notifiers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	defaultNotifyHTTPTimeout = 2 * time.Minute
	httpStatusClassDivisor   = 100
	httpStatusClassSuccess   = 2
)

type CustomWebhookNotifier struct {
	name    string
	url     string
	method  string
	headers map[string]string
	client  *http.Client
}

func NewCustomWebhookNotifier(name, url, method string, headers map[string]string) Notifier {
	customNotifier := newCustomWebhookNotifier(name, url, method, headers)

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return customNotifier
	}

	return NewTraceableNotifier(customNotifier, tp, nil)
}

func newCustomWebhookNotifier(name, url, method string, headers map[string]string) *CustomWebhookNotifier {
	if method == "" {
		method = http.MethodPost
	}
	return &CustomWebhookNotifier{
		name:    name,
		url:     url,
		method:  strings.ToUpper(method),
		headers: headers,
		client:  &http.Client{Timeout: defaultNotifyHTTPTimeout},
	}
}

func (n *CustomWebhookNotifier) Name() string {
	if n.name != "" {
		return "notifier-custom-" + n.name
	}
	return "notifier-custom"
}

func (*CustomWebhookNotifier) Kind() string {
	return "custom"
}

func (n *CustomWebhookNotifier) Notify(ctx context.Context, event Message) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, n.method, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, val := range n.headers {
		req.Header.Set(key, val)
	}

	//nolint:gosec // Destination URL is controlled by operator configuration for webhook notifications.
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/httpStatusClassDivisor != httpStatusClassSuccess {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	return nil
}
