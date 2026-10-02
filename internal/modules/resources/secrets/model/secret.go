package model

import "time"

// Secret contains persisted Docker secret metadata without its payload.
type Secret struct {
	// ID is the Docker secret identifier.
	ID string `json:"id"`
	// Name is the Docker secret name.
	Name string `json:"name"`
	// Description is copied from the Docker secret "description" label.
	Description string `json:"description,omitempty"`
	// VersionID is the Docker metadata version index.
	VersionID uint64 `json:"version_id"`
	// CreatedAt is the creation timestamp reported by Docker.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the last update timestamp reported by Docker.
	UpdatedAt time.Time `json:"updated_at"`
	// Driver is the external secret driver name.
	Driver string `json:"driver,omitempty"`
	// ExternalPath is the path in the external secret manager.
	ExternalPath string `json:"external_path,omitempty"`
	// ExternalVersionID is the version in the external secret manager.
	ExternalVersionID string `json:"external_version_id,omitempty"`
	// Managed is true when cloud-secrets manages the secret.
	Managed bool `json:"managed"`
	// Labels contains Docker secret metadata retained for API compatibility.
	Labels map[string]string `json:"labels,omitempty"`
}
