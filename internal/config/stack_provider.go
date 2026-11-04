package config

type StackProvider interface {
	// Stacks returns configured stack definitions.
	Stacks() []StackSpec
	// GitOpsStatusConfig returns the configuration which controls public GitOps status sections.
	GitOpsStatusConfig() GitOpsStatusConfig
}

// GitOpsStatusConfig controls which GitOps status projections are exposed.
type GitOpsStatusConfig struct {
	// SyncMode is the configured GitOps synchronization mode.
	SyncMode string
	// WebhookEnabled reports whether authenticated webhook processing is enabled.
	WebhookEnabled bool
}

func (c *Config) Stacks() []StackSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneStacks(c.stacks)
}

// GitOpsStatusConfig returns the static GitOps synchronization configuration.
func (c *Config) GitOpsStatusConfig() GitOpsStatusConfig {
	return GitOpsStatusConfig{
		SyncMode:       c.Spec.GitOps.Sync.Mode,
		WebhookEnabled: c.Spec.GitOps.Sync.Webhook.Enabled,
	}
}

// Networks returns a copy of the current accepted network definitions.
func (c *Config) Networks() []NetworkSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneNetworks(c.networks)
}
