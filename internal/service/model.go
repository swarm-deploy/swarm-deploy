package service

import (
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/service/stype"
	"github.com/swarm-deploy/webroute"
)

// Info is a persisted service metadata record.
type Info struct {
	// Name is a service name inside stack.
	Name string `json:"name"`
	// Stack is a docker stack name.
	Stack string `json:"stack"`
	// Description is a human-readable service description.
	Description string `json:"description,omitempty"`
	// Type is a service classification.
	Type serviceType.Type `json:"type"`
	// Image is a service container image reference.
	Image string `json:"image"`
	// RepositoryURL is a source repository URL resolved from service labels.
	RepositoryURL string `json:"repository_url,omitempty"`
	// WebRoutes is a list of public web routes resolved from service environment.
	WebRoutes []webroute.Route `json:"web_routes,omitempty"`
}
