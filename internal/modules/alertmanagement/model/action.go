package model

// AlertActionKind identifies a change to apply to the alert lifecycle.
type AlertActionKind string

const (
	RecordFailure             AlertActionKind = "record_failure"
	Resolve                   AlertActionKind = "resolve"
	ResetFailureStreak        AlertActionKind = "reset_failure_streak"
	ResetFailureStreaksExcept AlertActionKind = "reset_failure_streaks_except"
)

// AlertAction carries the data needed for one lifecycle change.
type AlertAction struct {
	// Kind identifies the change to apply.
	Kind AlertActionKind
	// Alert carries the incident to persist, or the fingerprint and resolution for other actions.
	// For RecordFailure, an empty ID identifies a new incident.
	Alert Alert
	// OpenAfter is the number of consecutive failures required to open an incident.
	OpenAfter uint64
	// TrackFailureStreak records consecutive failures across events.
	TrackFailureStreak bool
	// Findings lists diagnostics to add to the incident's existing findings.
	Findings []Finding
	// KeepFailureStreaks lists the desired stack fingerprints to retain during reconciliation.
	KeepFailureStreaks []string
}
