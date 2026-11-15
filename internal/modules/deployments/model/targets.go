package model

import (
	"slices"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// ServiceRevision identifies the effective state and source commit of one execution target.
type ServiceRevision struct {
	// Digest identifies the effective desired service state before redaction.
	Digest string `json:"digest"`
	// Commit is the source revision that introduced this service state.
	Commit string `json:"commit"`
}

// Targets reports whether the attempt included a service. Historical attempts applied the whole stack.
func (a Attempt) Targets(name string) bool {
	return a.TargetServices == nil || slices.Contains(a.TargetServices, name)
}

// Services returns the original execution scope, including compatibility with historical attempts.
func (a Attempt) Services(desired compose.File) compose.Services {
	return slices.DeleteFunc(slices.Clone(desired.Compose.Services), func(service compose.Service) bool {
		return !a.Targets(service.Name)
	})
}
