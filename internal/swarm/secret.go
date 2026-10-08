package swarm

import (
	"time"
)

const secretFileMode = 0o444

type Secret struct {
	// ID is a unique Docker secret identifier.
	ID string `json:"id"`
	// VersionID is a monotonic secret version index from Docker metadata.
	VersionID uint64 `json:"version_id"`
	// Name is a Docker secret name.
	Name string `json:"name"`
	// CreatedAt is a secret creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is a secret update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
	// Driver is an external secret driver name when configured.
	Driver string `json:"driver"`
	// Labels contains custom Docker secret labels.
	Labels map[string]string `json:"labels"`
}

// ListSecretsFilter selects Docker secrets by name, label, and stack ownership.
type ListSecretsFilter struct {
	// Names contains Docker secret names to match.
	Names []string
	// Labels contains Docker labels to match by key and value.
	Labels map[string]string
	// StackName selects secrets owned by the given stack.
	StackName string
}

// CreateSecretRequest describes a Docker secret to create.
type CreateSecretRequest struct {
	// Name is the Docker secret name.
	Name string
	// Labels contains custom Docker secret labels.
	Labels map[string]string
	// Data contains the secret payload.
	Data []byte
	// Driver is the external secret driver name.
	Driver string
	// DriverOptions contains external secret driver options.
	DriverOptions map[string]string
	// TemplateDriver is the secret template driver name.
	TemplateDriver string
}
