// Package notifications defines the public notification contract for security events.
package notifications

import "time"

// Payload is the stable public data contract for security notification templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// Username identifies the authenticated user when available.
	Username string `json:"username"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReferencePayload returns representative authenticated-user data.
func ReferencePayload() Payload {
	return Payload{ID: "event-user-authenticated", Type: "userAuthenticated", Username: "operator",
		Message: "User operator authenticated", OccurredAt: time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)}
}
