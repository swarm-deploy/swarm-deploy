package swarm

import (
	"errors"

	dockernetwork "github.com/docker/docker/api/types/network"
)

// ErrNetworkNotFound means that network does not exist in docker.
var ErrNetworkNotFound = errors.New("network not found")

// ListNetworksFilter selects Docker networks by stack ownership.
type ListNetworksFilter struct {
	// Stacks contains exact stack names combined with OR.
	Stacks []string
}

// Network is a runtime snapshot of Docker network metadata.
type Network struct {
	ID string `json:"id"`
	// Name is a Docker network name.
	Name string `json:"name"`
	// Scope describes where network exists (for example: local or swarm).
	Scope string `json:"scope"`
	// Driver is a Docker network driver name.
	Driver string `json:"driver"`
	// Internal indicates that network is internal-only.
	Internal bool `json:"internal"`
	// Attachable indicates network can be attached by standalone containers.
	Attachable bool `json:"attachable"`
	// Ingress indicates swarm routing-mesh ingress network.
	Ingress bool `json:"ingress"`
	// Labels contains custom Docker network labels.
	Labels map[string]string `json:"labels"`
	// Options contains driver-specific network options.
	Options map[string]string `json:"options"`
	// Stack name of stack, computed from labels,
	Stack string `json:"stack,omitempty"`
}

type CreateNetworkRequest struct {
	// IPAM configures network address allocation.
	IPAM *dockernetwork.IPAM `json:"ipam,omitempty" yaml:"ipam,omitempty"`
	// Name is a Docker network name.
	Name string `json:"name" yaml:"name"`
	// Driver is the driver-name used to create the network (e.g. `bridge`, `overlay`)
	Driver string `json:"driver" yaml:"driver"`
	// Attachable allows standalone containers to attach to the network.
	Attachable bool `json:"attachable" yaml:"attachable"`
	// Internal marks network as internal-only.
	Internal bool `json:"internal" yaml:"internal"`
	// Options specifies the network-specific options to use for when creating the network.
	Options map[string]string `json:"options" yaml:"options"`
	// Labels contains custom Docker network labels.
	Labels map[string]string `json:"labels" yaml:"labels"`
}

// ServiceNetworkScope accepts Swarm networks and the Engine's predefined host/none
// external networks. Arbitrary local bridge networks cannot be attached to services.
func ServiceNetworkScope(name, scope string, external bool) bool {
	return scope == "swarm" || (external && scope == "local" && (name == "host" || name == "none"))
}
