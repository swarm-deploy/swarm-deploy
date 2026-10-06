package model

import "time"

type AlertKind string

const AlertKindDeployFailed AlertKind = "deploy_failed"

type ResourceType string

const ResourceTypeStack ResourceType = "stack"

type AlertStatus string

const (
	AlertStatusOpen     AlertStatus = "open"
	AlertStatusResolved AlertStatus = "resolved"
)

type ResolutionReason string

const ResolutionReasonRecovered ResolutionReason = "recovered"

// AlertResolution describes how an alert incident ended.
type AlertResolution struct {
	// Reason is the machine-readable resolution reason.
	Reason ResolutionReason `json:"reason"`
	// Message is a human-readable resolution summary.
	Message string `json:"message"`
	// EventID identifies the event that resolved the alert.
	EventID string `json:"eventId"`
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

// DeployFailedFingerprint returns the correlation key for a stack deployment failure.
func DeployFailedFingerprint(stackID string) string {
	return string(AlertKindDeployFailed) + ":" + string(ResourceTypeStack) + ":" + stackID
}
