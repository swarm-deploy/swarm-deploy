package events

import "strconv"

// WebhookReceived is emitted for each authenticated incoming webhook.
type WebhookReceived struct {
	// Queued indicates whether the webhook triggered a queued reconciliation.
	Queued bool
}

func (w *WebhookReceived) Type() Type {
	return TypeWebhookReceived
}

func (w *WebhookReceived) Message() string {
	return "Webhook received"
}

func (w *WebhookReceived) Details() map[string]string {
	return map[string]string{
		"queued": strconv.FormatBool(w.Queued),
	}
}
