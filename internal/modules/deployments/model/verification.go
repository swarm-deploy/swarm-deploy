package model

import "time"

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
}
