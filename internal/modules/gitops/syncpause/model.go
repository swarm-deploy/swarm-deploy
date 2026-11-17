// Package syncpause owns persistent GitOps synchronization pause policy.
package syncpause

import "time"

// Scope identifies the direct pause target.
type Scope string

const (
	// ScopeStack applies to every GitOps-managed service in a stack.
	ScopeStack Scope = "stack"
	// ScopeService applies to one GitOps-managed service.
	ScopeService Scope = "service"
)

// EffectiveSource identifies which direct pause records make a service paused.
type EffectiveSource string

const (
	// EffectiveSourceNone means neither pause scope is active.
	EffectiveSourceNone EffectiveSource = "none"
	// EffectiveSourceStack means only the stack pause is active.
	EffectiveSourceStack EffectiveSource = "stack"
	// EffectiveSourceService means only the service pause is active.
	EffectiveSourceService EffectiveSource = "service"
	// EffectiveSourceBoth means stack and service pauses are both active.
	EffectiveSourceBoth EffectiveSource = "both"
)

// Pause is one direct persisted pause. It contains policy only, never service state.
type Pause struct {
	// Scope identifies whether StackName or StackName/ServiceName is paused.
	Scope Scope
	// StackName identifies the GitOps stack.
	StackName string
	// ServiceName is set only for service scope.
	ServiceName string
	// ExpiresAt is nil for a pause without expiration.
	ExpiresAt *time.Time
}

// StackState contains active direct pauses and calculated managed service counts.
type StackState struct {
	// StackPause is the active direct stack pause, if any.
	StackPause *Pause
	// ServicePauses contains active direct service pauses, including pauses masked by StackPause.
	ServicePauses []Pause
	// ManagedServices contains service names from GitOps runtime state.
	ManagedServices []string
	// EffectivelyPausedServices contains managed service names paused by either scope.
	EffectivelyPausedServices []string
}

// ServiceState contains direct and effective state for one GitOps-managed service.
type ServiceState struct {
	// StackPause is the active direct stack pause, if any.
	StackPause *Pause
	// ServicePause is the active direct service pause, if any.
	ServicePause *Pause
	// Effective reports whether either direct pause applies.
	Effective bool
	// Source identifies the active direct pause sources.
	Source EffectiveSource
}

// ActivePolicy is the active pause policy consumed by reconciliation.
type ActivePolicy struct {
	// StackPause is the active direct stack pause, if any.
	StackPause *Pause
	// ServicePauses is keyed by service name.
	ServicePauses map[string]Pause
}

// Preserves reports whether a service must be skipped by reconciliation.
func (p ActivePolicy) Preserves(service string) bool {
	if p.StackPause != nil {
		return true
	}
	_, paused := p.ServicePauses[service]
	return paused
}

// Source returns the effective source for a service.
func (p ActivePolicy) Source(service string) EffectiveSource {
	_, servicePaused := p.ServicePauses[service]
	switch {
	case p.StackPause != nil && servicePaused:
		return EffectiveSourceBoth
	case p.StackPause != nil:
		return EffectiveSourceStack
	case servicePaused:
		return EffectiveSourceService
	default:
		return EffectiveSourceNone
	}
}
