// Package notifications defines the public notification contract for assistant security events.
package notifications

import "time"

// Payload is the stable public data contract for assistant notification templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Detector identifies the prompt-injection detector.
	Detector string `json:"detector"`
	// Username identifies the user when available.
	Username string `json:"username"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReferencePayload returns representative prompt-injection data.
func ReferencePayload() Payload {
	return Payload{ID: "event-prompt-injection", Type: "assistantPromptInjectionDetected", Detector: "regexp",
		Username: "operator", Message: "Detected prompt injection",
		OccurredAt: time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)}
}
