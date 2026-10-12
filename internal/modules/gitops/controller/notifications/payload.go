// Package notifications defines public notification contracts for reconciliation events.
package notifications

import "time"

// Payload is the stable public data contract for reconciliation notification templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// StackName identifies an affected stack when available.
	StackName string `json:"stack_name"`
	// TriggeredBy identifies the user who started a manual sync.
	TriggeredBy string `json:"triggered_by"`
	// Queued reports whether a webhook scheduled reconciliation.
	Queued bool `json:"queued"`
	// NetworkName identifies a created network.
	NetworkName string `json:"network_name"`
	// NetworkID identifies a created Docker Swarm network.
	NetworkID string `json:"network_id"`
	// Driver is the created network driver.
	Driver string `json:"driver"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReferencePayload returns representative data for a reconciliation event type.
func ReferencePayload(eventType string) Payload {
	payload := Payload{ID: "event-reconciliation", Type: eventType, StackName: "payments", TriggeredBy: "operator",
		Queued: true, NetworkName: "payments", NetworkID: "network-42", Driver: "overlay",
		OccurredAt: time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)}
	switch eventType {
	case "serviceCatalogUpdated":
		payload.Message = "Service catalog updated"
	case "syncManualStarted":
		payload.Message = "Manual sync started"
	case "webhookReceived":
		payload.Message = "Webhook received"
	case "networkCreated":
		payload.Message = "Network payments created"
	default:
		payload.Message = "Reconciliation event"
	}
	return payload
}
