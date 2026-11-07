package model

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

type AlertStatus string

const (
	AlertStatusOpen     AlertStatus = "open"
	AlertStatusResolved AlertStatus = "resolved"
)

// Finding is a typed diagnostic recorded during an alert's lifetime.
type Finding struct {
	// Code identifies the diagnostic cause.
	Code faults.Code `json:"code"`
	// Summary contains the sanitized diagnostic.
	Summary string `json:"summary"`
	// DetectedAt is when the finding was first recorded.
	DetectedAt time.Time `json:"detectedAt"`
}

// Alert is one persisted incident for a correlated problem.
type Alert struct {
	// ID uniquely identifies this incident.
	ID string `json:"id"`
	// Fingerprint correlates events for the same active problem.
	Fingerprint string `json:"fingerprint"`
	// Kind identifies the alert kind.
	Kind AlertKind `json:"kind"`
	// ResourceType identifies the affected resource kind.
	ResourceType ResourceType `json:"resourceType"`
	// ResourceID identifies the affected resource.
	ResourceID string `json:"resourceId"`
	// Status is the current alert lifecycle state.
	Status AlertStatus `json:"status"`
	// Title is the short human-readable alert title.
	Title string `json:"title"`
	// Message contains the latest useful problem detail.
	Message string `json:"message"`
	// FailureStage identifies the pipeline stage involved, when known.
	FailureStage string `json:"failureStage,omitempty"`
	// ErrorCode is stable technical metadata for the failure.
	ErrorCode string `json:"errorCode,omitempty"`
	// Findings contains the distinct typed diagnostics recorded for this alert.
	Findings []Finding `json:"findings,omitempty"`
	// DeploymentID links to the related attempt when one was created.
	DeploymentID string `json:"deploymentId,omitempty"`
	// Commit identifies the source revision involved in the failure.
	Commit string `json:"commit,omitempty"`
	// Occurrences counts correlated failure events.
	Occurrences uint64 `json:"occurrences"`
	// OpenedAt is when this incident started.
	OpenedAt time.Time `json:"openedAt"`
	// UpdatedAt is when this incident last changed.
	UpdatedAt time.Time `json:"updatedAt"`
	// OpenEventID identifies the event that opened the incident.
	OpenEventID string `json:"openEventId"`
	// LatestEventID identifies the latest failure event.
	LatestEventID string `json:"latestEventId"`
	// ResolvedAt is when the incident recovered.
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	// Resolution describes how the incident ended.
	Resolution *AlertResolution `json:"resolution,omitempty"`
}
