// Package notifications defines the public notification contract for Swarm nodes.
package notifications

import "time"

// Payload is the stable public data contract for node notification templates.
type Payload struct {
	// ID identifies the source event delivery.
	ID string `json:"id"`
	// Type is the configured notification event type.
	Type string `json:"type"`
	// NodeID is the Docker Swarm node identifier.
	NodeID string `json:"node_id"`
	// NodeName is the Docker Swarm node hostname.
	NodeName string `json:"node_name"`
	// Role is the node role when available.
	Role string `json:"role"`
	// Status is the observed node status when available.
	Status string `json:"status"`
	// Message is the human-readable event summary.
	Message string `json:"message"`
	// OccurredAt is the event publication time in UTC.
	OccurredAt time.Time `json:"occurred_at"`
}

// JoinedReferencePayload returns representative node-joined data.
func JoinedReferencePayload() Payload {
	return Payload{ID: "event-node-joined", Type: "nodeJoined", NodeID: "node-1", NodeName: "worker-1",
		Role: "worker", Message: "Node worker-1 joined the swarm", OccurredAt: referenceTime()}
}

// ConnectedReferencePayload returns representative node-connected data.
func ConnectedReferencePayload() Payload {
	return Payload{ID: "event-node-connected", Type: "nodeConnected", NodeID: "node-1", NodeName: "worker-1",
		Status: "ready", Message: "Node worker-1 connected", OccurredAt: referenceTime()}
}

// DisconnectedReferencePayload returns representative node-disconnected data.
func DisconnectedReferencePayload() Payload {
	return Payload{ID: "event-node-disconnected", Type: "nodeDisconnected", NodeID: "node-1", NodeName: "worker-1",
		Status: "down", Message: "Node worker-1 disconnected", OccurredAt: referenceTime()}
}

func referenceTime() time.Time {
	return time.Date(2026, time.October, 13, 9, 30, 0, 0, time.UTC)
}
