package config

func cloneStacks(stacks []StackSpec) []StackSpec {
	cloned := append([]StackSpec(nil), stacks...)
	for i := range cloned {
		if stacks[i].Sync.Policy.Prune != nil {
			prune := *stacks[i].Sync.Policy.Prune
			cloned[i].Sync.Policy.Prune = &prune
		}
		if stacks[i].Sync.Policy.SelfHeal != nil {
			selfHeal := *stacks[i].Sync.Policy.SelfHeal
			cloned[i].Sync.Policy.SelfHeal = &selfHeal
		}
	}
	return cloned
}

func cloneNetworks(networks []NetworkSpec) []NetworkSpec {
	cloned := append([]NetworkSpec(nil), networks...)
	for i := range cloned {
		cloned[i].Labels = cloneStringMap(networks[i].Labels)
		cloned[i].Options = cloneStringMap(networks[i].Options)
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
