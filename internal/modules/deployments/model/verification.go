package model

// Verification records whether rollout obligations are known independently of list allocation.
type Verification struct {
	// Known distinguishes a verified obligation set from an interrupted or legacy unknown outcome.
	Known bool
	// UnresolvedServices contains services that still require proof of convergence.
	UnresolvedServices []string
}
