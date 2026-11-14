package model

// AlertActionKind identifies a change to apply to the alert lifecycle.
type AlertActionKind string

const (
	AlertActionRecordFailure             AlertActionKind = "record_failure"
	AlertActionResolve                   AlertActionKind = "resolve"
	AlertActionResetFailureStreak        AlertActionKind = "reset_failure_streak"
	AlertActionResetFailureStreaksExcept AlertActionKind = "reset_failure_streaks_except"
)

// AlertAction carries the data needed for one lifecycle change.
// A record-failure action carries a snapshot evaluated in the same transaction as Apply.
// A batch containing that snapshot must not include another incident write for its fingerprint.
// Counter resets do not write incidents.
type AlertAction struct {
	// Kind identifies the change to apply.
	Kind AlertActionKind
	// Alert carries the incident to persist, or the fingerprint and resolution for other actions.
	// For a failure, an empty ID identifies a new incident.
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

// NewRecordFailureAction records a failure with the given opening threshold and findings.
func NewRecordFailureAction(alert Alert, openAfter uint64, trackFailureStreak bool, findings ...Finding) AlertAction {
	return AlertAction{
		Kind: AlertActionRecordFailure, Alert: alert, OpenAfter: openAfter,
		TrackFailureStreak: trackFailureStreak, Findings: findings,
	}
}

// NewResolveAction resolves the incident identified by fingerprint if it is open.
func NewResolveAction(fingerprint, eventID string, reason ResolutionReason, message string) AlertAction {
	return AlertAction{
		Kind: AlertActionResolve,
		Alert: Alert{
			Fingerprint: fingerprint,
			Resolution:  &AlertResolution{Reason: reason, Message: message, EventID: eventID},
		},
	}
}

// NewResetFailureStreakAction clears the streak for one fingerprint.
func NewResetFailureStreakAction(fingerprint string) AlertAction {
	return AlertAction{
		Kind:  AlertActionResetFailureStreak,
		Alert: Alert{Fingerprint: fingerprint},
	}
}

// NewResetFailureStreaksExceptAction clears stack streaks outside the retained fingerprints.
func NewResetFailureStreaksExceptAction(fingerprints []string) AlertAction {
	return AlertAction{Kind: AlertActionResetFailureStreaksExcept, KeepFailureStreaks: fingerprints}
}
