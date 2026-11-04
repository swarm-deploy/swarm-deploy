package model

import "time"

// VerificationMode describes which tasks a service transition can preserve.
type VerificationMode uint8

const (
	// VerifyUnspecified retains compatibility with receipts written before modes were persisted.
	VerifyUnspecified VerificationMode = iota
	// VerifyExistingTasks permits tasks with the expected template.
	VerifyExistingTasks
	// VerifyPlacementCompatibleTasks also permits older placement on compatible nodes.
	VerifyPlacementCompatibleTasks
	// VerifyNewGeneration requires replacement tasks.
	VerifyNewGeneration
)

// Verification records whether rollout obligations are known independently of list allocation.
type Verification struct {
	// Known distinguishes a verified obligation set from an interrupted or legacy unknown outcome.
	Known bool
	// UnresolvedServices contains services that still require proof of convergence.
	UnresolvedServices []string
	// Generations retains pre-update boundaries for unresolved services, without service secrets.
	Generations map[string]UpdateGeneration
}

// UpdateGeneration identifies a service write across interrupted verification.
type UpdateGeneration struct {
	// ServiceID identifies the service being updated.
	ServiceID string `json:"service_id"`
	// ForceUpdate is the execution generation preserved in the accepted spec.
	ForceUpdate uint64 `json:"force_update"`
	// After is the last inspected service timestamp before the write.
	After time.Time `json:"after"`
	// Mode retains the verification obligation when a later write replaces PreviousSpec.
	Mode VerificationMode `json:"mode,omitempty"`
	// BeforeSpecDigest identifies the original predecessor without persisting its contents.
	BeforeSpecDigest string `json:"before_spec_digest,omitempty"`
	// TasksAfter preserves the original task boundary when a non-replacing write clears
	// its UpdateStatus. Zero means a fresh rollout status is still required.
	TasksAfter time.Time `json:"tasks_after,omitzero"`
}
