package inspector

import (
	"sort"
	"time"
)

// SecretInfo is a runtime snapshot of Docker secret metadata.
type SecretInfo struct {
	// ID is a unique Docker secret identifier.
	ID string `json:"id"`
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

func sortSecretInfos(secrets []SecretInfo) {
	sort.Slice(secrets, func(i, j int) bool {
		if secrets[i].Name != secrets[j].Name {
			return secrets[i].Name < secrets[j].Name
		}

		return secrets[i].ID < secrets[j].ID
	})
}
