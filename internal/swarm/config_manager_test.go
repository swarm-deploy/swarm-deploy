package swarm

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func TestConfigManagerMapConfigMapsFields(t *testing.T) {
	createdAt := time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(5 * time.Minute)

	config := dockerswarm.Config{
		ID: "config-id",
		Meta: dockerswarm.Meta{
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		Spec: dockerswarm.ConfigSpec{
			Annotations: dockerswarm.Annotations{
				Name: "app-config",
				Labels: map[string]string{
					"com.example.env": "prod",
				},
			},
		},
	}

	mapped := (&ConfigManager{}).mapConfig(config)

	assert.Equal(t, "config-id", mapped.ID, "unexpected config id")
	assert.Equal(t, "app-config", mapped.Name, "unexpected config name")
	assert.Equal(t, createdAt, mapped.CreatedAt, "unexpected created at")
	assert.Equal(t, updatedAt, mapped.UpdatedAt, "unexpected updated at")
	assert.Equal(t, map[string]string{"com.example.env": "prod"}, mapped.Labels, "unexpected labels")
}
