package model

import "slices"

// DigestAlgorithmVersion identifies the canonical effective-state digest algorithm.
// A version change intentionally makes prior applied evidence pending again.
const DigestAlgorithmVersion = 1

// PendingState is the exact set of desired objects that still need confirmation.
type PendingState struct {
	// Services contains desired services whose desired and applied digests differ.
	Services []string
	// Removals contains formerly applied services that are absent from desired state.
	Removals []string
	// StackResources reports pending networks, configs, secrets, or other stack-scoped resources.
	StackResources bool
}

// Empty reports whether desired and applied state fully agree.
func (p PendingState) Empty() bool {
	return len(p.Services) == 0 && len(p.Removals) == 0 && !p.StackResources
}

// ServiceHashState separates one service's current desired and confirmed applied identities.
type ServiceHashState struct {
	// Name identifies the Compose service.
	Name string
	// DesiredDigest is empty when the service has been removed from desired state.
	DesiredDigest string
	// DesiredVersion identifies the desired digest algorithm; zero means absent.
	DesiredVersion int
	// DesiredCommit is diagnostic only and never participates in admission.
	DesiredCommit string
	// AppliedDigest is empty until application has been confirmed or after confirmed removal.
	AppliedDigest string
	// AppliedVersion identifies the applied digest algorithm; zero means unknown or absent.
	AppliedVersion int
	// AppliedCommit is diagnostic only and never participates in admission.
	AppliedCommit string
	// AppliedUncertain means an unsuccessful attempt may have changed runtime
	// after the last confirmed applied hash and requires explicit reconfirmation.
	AppliedUncertain bool
}

// HashState is the durable desired/applied synchronization state for one stack.
type HashState struct {
	// Stack identifies the deployment target.
	Stack string
	// DesiredDigest identifies the complete current effective desired state.
	DesiredDigest string
	// DesiredVersion identifies the desired digest algorithm.
	DesiredVersion int
	// DesiredCommit is diagnostic only and never participates in admission.
	DesiredCommit string
	// AppliedDigest identifies the last completely confirmed stack state.
	AppliedDigest string
	// AppliedVersion identifies the applied stack digest algorithm; zero means unknown.
	AppliedVersion int
	// AppliedCommit is diagnostic only and never participates in admission.
	AppliedCommit string
	// DesiredResourcesDigest identifies current stack-scoped resources.
	DesiredResourcesDigest string
	// DesiredResourcesVersion identifies their digest algorithm.
	DesiredResourcesVersion int
	// AppliedResourcesDigest identifies confirmed stack-scoped resources.
	AppliedResourcesDigest string
	// AppliedResourcesVersion identifies their applied digest algorithm; zero means unknown.
	AppliedResourcesVersion int
	// ResourcesUncertain means an unsuccessful attempt may have changed stack resources.
	ResourcesUncertain bool
	// Services stores desired/applied state keyed by Compose service name.
	Services map[string]ServiceHashState
}

// Pending returns a deterministic list of all desired/applied mismatches.
func (s HashState) Pending() PendingState {
	pending := PendingState{Services: []string{}, Removals: []string{}}
	for name, service := range s.Services {
		switch {
		case service.DesiredDigest == "" && (service.AppliedDigest != "" || service.AppliedUncertain):
			pending.Removals = append(pending.Removals, name)
		case service.DesiredDigest != "" &&
			(service.AppliedUncertain || service.DesiredDigest != service.AppliedDigest ||
				service.DesiredVersion != service.AppliedVersion):
			pending.Services = append(pending.Services, name)
		}
	}
	slices.Sort(pending.Services)
	slices.Sort(pending.Removals)
	pending.StackResources = s.ResourcesUncertain || s.DesiredResourcesDigest != s.AppliedResourcesDigest ||
		s.DesiredResourcesVersion != s.AppliedResourcesVersion
	if len(pending.Services) == 0 && len(pending.Removals) == 0 && !pending.StackResources &&
		(s.DesiredDigest != s.AppliedDigest || s.DesiredVersion != s.AppliedVersion) {
		// A complete-stack marker can be unknown even when object rows happen to
		// agree (for example after conservative bootstrap). Reconfirm safely.
		pending.StackResources = true
	}
	return pending
}
