package config

type StackProvider interface {
	// Stacks returns configured stack definitions.
	Stacks() []StackSpec
}

func (c *Config) Stacks() []StackSpec {
	return c.Spec.Stacks
}
