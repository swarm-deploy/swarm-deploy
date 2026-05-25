package compose

type Network struct {
	Name     string `yaml:"name" json:"name"`
	External bool   `yaml:"external" json:"external"`
	Internal *bool  `yaml:"internal,omitempty" json:"internal,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

func resolveNetworkAliases(networks []*ServiceNetwork, namesByAlias map[string]Network) {
	if len(networks) == 0 || len(namesByAlias) == 0 {
		return
	}

	for _, network := range networks {
		resolved, ok := namesByAlias[network.Alias]
		if ok && resolved.Name != "" {
			continue
		}

		network.Name = resolved.Name
	}
}
