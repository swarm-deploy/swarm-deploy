package compose

type Network struct {
	Name     string `yaml:"name" json:"name"`
	External bool   `yaml:"external" json:"external"`
	Internal *bool  `yaml:"internal,omitempty" json:"internal,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
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
