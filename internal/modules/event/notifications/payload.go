// Package notifications defines the public contract for Event Bus notifications.
package notifications

import "time"

// Payload is the stable public data contract for notification-delivery events.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// EventType identifies the event whose delivery failed.
	EventType string `json:"event_type"`
	// Destination identifies the notifier kind.
	Destination string `json:"destination"`
	// Channel identifies the configured notification channel.
	Channel string `json:"channel"`
	// Error is the safe delivery diagnostic when available.
	Error string `json:"error"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReferencePayload returns representative delivery-failure data.
func ReferencePayload() Payload {
	return Payload{ID: "event-notification-failed", Type: "SendNotificationFailed", EventType: "DeployFailed",
		Destination: "telegram", Channel: "ops", Error: "request timeout", Message: "Failed to send notification",
		OccurredAt: time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)}
}
