package swarm

import "errors"

// ErrNetworkNotFound means that network does not exist in docker.
var ErrNetworkNotFound = errors.New("network not found")

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
}

type CreateNetworkRequest struct {
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
