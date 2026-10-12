// Package notifications defines the public notification contract for services.
package notifications

import "time"

// Payload is the stable public data contract for service notification templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// StackName identifies the stack containing the service.
	StackName string `json:"stack_name"`
	// ServiceName identifies the service without a stack prefix.
	ServiceName string `json:"service_name"`
	// Commit identifies the source revision when available.
	Commit string `json:"commit"`
	// Username identifies the user who initiated the action when available.
	Username string `json:"username"`
	// PreviousReplicas is the former desired replica count.
	PreviousReplicas uint64 `json:"previous_replicas"`
	// CurrentReplicas is the new desired replica count.
	CurrentReplicas uint64 `json:"current_replicas"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// ReferencePayload returns representative data for a service event type.
func ReferencePayload(eventType string) Payload {
	const (
		referencePreviousReplicas = 2
		referenceCurrentReplicas  = 3
	)

	payload := Payload{ID: "event-service", Type: eventType, StackName: "payments", ServiceName: "api",
		Commit: "a1b2c3d4", Username: "operator",
		PreviousReplicas: referencePreviousReplicas, CurrentReplicas: referenceCurrentReplicas,
		OccurredAt: time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)}
	switch eventType {
	case "serviceMissed":
		payload.Message = "Service payments/api missed"
	case "servicePruned":
		payload.Message = "Service payments/api pruned"
	case "serviceReplicasIncreased":
		payload.Message = "Service payments/api replicas increased from 2 to 3"
	case "serviceReplicasDecreased":
		payload.PreviousReplicas, payload.CurrentReplicas = referenceCurrentReplicas, referencePreviousReplicas
		payload.Message = "Service payments/api replicas decreased from 3 to 2"
	case "serviceRestarted":
		payload.Message = "Service payments/api restarted"
	default:
		payload.Message = "Service event"
	}
	return payload
}
