package compose

type Network struct {
	Name     string `yaml:"name" json:"name"`
	External bool   `yaml:"external" json:"external"`
	Internal bool   `yaml:"internal" json:"internal"`
}

func resolveNetworkAliases(networks []string, namesByAlias map[string]Network) []string {
	if len(networks) == 0 {
		return nil
	}

	if len(namesByAlias) == 0 {
		return networks
	}

	out := make([]string, 0, len(networks))
	for _, network := range networks {
		resolved, ok := namesByAlias[network]
		if ok && resolved.Name != "" {
			out = append(out, resolved.Name)
			continue
		}
		out = append(out, network)
	}
	return out
}
