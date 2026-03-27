package inspector

import (
	"sort"
	"strings"
)

// NetworkInfo is a runtime snapshot of Docker network metadata.
type NetworkInfo struct {
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

func normalizeNetworkInfo(network NetworkInfo) NetworkInfo {
	network.Name = strings.TrimSpace(network.Name)
	network.Scope = strings.TrimSpace(network.Scope)
	network.Driver = strings.TrimSpace(network.Driver)

	return network
}

func sortNetworkInfos(networks []NetworkInfo) {
	sort.Slice(networks, func(i, j int) bool {
		if networks[i].Name != networks[j].Name {
			return networks[i].Name < networks[j].Name
		}
		if networks[i].Scope != networks[j].Scope {
			return networks[i].Scope < networks[j].Scope
		}

		return networks[i].Driver < networks[j].Driver
	})
}
