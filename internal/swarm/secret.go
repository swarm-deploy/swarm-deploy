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
