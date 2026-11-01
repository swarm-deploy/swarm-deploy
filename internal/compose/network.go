package compose

type Network struct {
	// IPAM configures network address allocation.
	IPAM *NetworkIPAM `yaml:"ipam,omitempty" json:"ipam,omitempty"`
	// Driver selects the network driver; empty means overlay for stacks.
	Driver string `yaml:"driver,omitempty" json:"driver,omitempty"`
	// DriverOpts contains driver-specific network options.
	DriverOpts map[string]string `yaml:"driver_opts,omitempty" json:"driver_opts,omitempty"`
	// Attachable allows standalone containers to use the network.
	Attachable bool `yaml:"attachable,omitempty" json:"attachable,omitempty"`
	// Labels contains network metadata.
	Labels   Labels `yaml:"labels,omitempty" json:"labels,omitempty"`
	Name     string `yaml:"name" json:"name"`
	External bool   `yaml:"external" json:"external"`
	Internal *bool  `yaml:"internal,omitempty" json:"internal,omitempty"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

func resolveNetworkAliases(networks *ServiceNetworks, namesByAlias map[string]Network) {
	if networks == nil {
		return
	}

	if len(networks.List) == 0 || len(namesByAlias) == 0 {
		return
	}

	networkSet := map[string]struct{}{}

	for _, network := range networks.List {
		resolved, ok := namesByAlias[network.Alias]
		if !ok || resolved.Name == "" {
			continue
		}

		network.ResolvedName = resolved.Name

		if _, exists := networkSet[resolved.Name]; !exists {
			networks.Names = append(networks.Names, resolved.Name)
			networkSet[resolved.Name] = struct{}{}
		}
	}
}
