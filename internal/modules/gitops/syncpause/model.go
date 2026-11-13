// Package syncpause owns persistent GitOps synchronization pause policy.
package syncpause

import "time"

// Pause is one persisted stack pause. It contains policy only, never service state.
type Pause struct {
	// StackName identifies the GitOps stack.
	StackName string
	// ExpiresAt is nil for a pause without expiration.
	ExpiresAt *time.Time
}

// StackState contains the active pause and calculated managed service state.
type StackState struct {
	// StackPause is the active stack pause, if any.
	StackPause *Pause
	// ManagedServices contains service names from GitOps runtime state.
	ManagedServices []string
	// EffectivelyPausedServices contains managed service names paused with the stack.
	EffectivelyPausedServices []string
}

// ActivePolicy is the active pause policy consumed by reconciliation.
type ActivePolicy struct {
	// StackPause is the active stack pause, if any.
	StackPause *Pause
}

// Preserves reports whether stack reconciliation must preserve its services.
func (p ActivePolicy) Preserves() bool { return p.StackPause != nil }
