package swarm

import "time"

const configFileMode = 0o444

// Config is a runtime snapshot of Docker config metadata.
type Config struct {
	// ID is a unique Docker config identifier.
	ID string `json:"id"`
	// Name is a Docker config name.
	Name string `json:"name"`
	// CreatedAt is a config creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is a config update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
	// Labels contains custom Docker config labels.
	Labels map[string]string `json:"labels"`
	// Data contains config payload.
	Data []byte `json:"data,omitempty"`
}

// ServiceConfig is a config mounted into a service container.
type ServiceConfig struct {
	// ConfigID is a Docker config identifier.
	ConfigID string `json:"config_id,omitempty"`
	// ConfigName is a Docker config name.
	ConfigName string `json:"config_name"`
	// Target is a target file path inside container.
	Target string `json:"target,omitempty"`
	// Data contains config payload.
	Data []byte `json:"data,omitempty"`
}
