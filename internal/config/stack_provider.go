package config

type StackProvider interface {
	// Stacks returns configured stack definitions.
	Stacks() []StackSpec
}

func (c *Config) Stacks() []StackSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneStacks(c.stacks)
}

// Networks returns a copy of the current accepted network definitions.
func (c *Config) Networks() []NetworkSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneNetworks(c.networks)
}
