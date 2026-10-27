package model

type ResolutionReason string

const (
	ResolutionReasonRecovered       ResolutionReason = "recovered"
	ResolutionReasonResourceRemoved ResolutionReason = "resource_removed"
)

// AlertResolution describes how an alert incident ended.
type AlertResolution struct {
	// Reason is the machine-readable resolution reason.
	Reason ResolutionReason `json:"reason"`
	// Message is a human-readable resolution summary.
	Message string `json:"message"`
	// EventID identifies the event that resolved the alert.
	EventID string `json:"eventId"`
}
