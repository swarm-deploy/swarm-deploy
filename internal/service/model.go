package service

import serviceType "github.com/artarts36/swarm-deploy/internal/service/stype"

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
}
