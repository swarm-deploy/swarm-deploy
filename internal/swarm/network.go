package swarm

// Network is a runtime snapshot of Docker network metadata.
type Network struct {
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
}
