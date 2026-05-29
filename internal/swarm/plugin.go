package swarm

// Plugin is a runtime snapshot of Docker plugin metadata.
type Plugin struct {
	// ID is a unique Docker plugin identifier.
	ID string `json:"id"`
	// Name is a Docker plugin name.
	Name string `json:"name"`
	// Description is a plugin description from plugin config.
	Description string `json:"description"`
	// Enabled indicates whether plugin is enabled.
	Enabled bool `json:"enabled"`
	// PluginReference is a plugin reference used for push/pull.
	PluginReference string `json:"plugin_reference"`
	// Capabilities contains plugin interface capabilities.
	Capabilities []string `json:"capabilities"`
}
