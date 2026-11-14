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
	// Alert carries the fingerprint, failure data or resolution for the change.
	Alert Alert
	// OpenAfter is the number of consecutive failures required to open an incident.
	OpenAfter uint64
	// TrackFailureStreak records consecutive failures across events.
	TrackFailureStreak bool
	// PreserveExistingMessage keeps useful detail when a repeat has no diagnostic.
	PreserveExistingMessage bool
	// KeepFailureStreaks lists the desired stack fingerprints to retain during reconciliation.
	KeepFailureStreaks []string
}
