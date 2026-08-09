package service

import (
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
)

// Info is a persisted service metadata record.
type Info struct {
	// Metadata is resolved service metadata.
	metadata.Metadata `json:",inline"`

	// Name is a service name inside stack.
	Name string `json:"name"`
	// Stack is a docker stack name.
	Stack string `json:"stack"`
	// Image is a service container image reference.
	Image string `json:"image"`
	// Environment is a resolved container environment snapshot.
	Environment map[string]string `json:"environment,omitempty"`
	// Spec is a compact persisted service spec snapshot.
	Spec swarm.ServiceSpec `json:"spec"`
	// WebRoutes is a list of public web routes resolved from service environment.
	WebRoutes []webroute.Route `json:"web_routes,omitempty"`
}

func (i *Info) GetName() string {
	return i.Name
}
