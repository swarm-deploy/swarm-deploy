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
			Data: []byte("routes: []"),
			Annotations: dockerswarm.Annotations{
				Name: "app-config",
				Labels: map[string]string{
					"com.example.env": "prod",
				},
			},
		},
	}

	mapped := (&configManager{}).mapConfig(config)

	assert.Equal(t, "config-id", mapped.ID, "unexpected config id")
	assert.Equal(t, "app-config", mapped.Name, "unexpected config name")
	assert.Equal(t, createdAt, mapped.CreatedAt, "unexpected created at")
	assert.Equal(t, updatedAt, mapped.UpdatedAt, "unexpected updated at")
	assert.Equal(t, map[string]string{"com.example.env": "prod"}, mapped.Labels, "unexpected labels")
	assert.Equal(t, []byte("routes: []"), mapped.Data, "unexpected data")

	config.Spec.Data[0] = 'R'
}

func TestBuildConfigListFilters(t *testing.T) {
	tests := []struct {
		name           string
		filter         ListConfigsFilter
		expectedNames  []string
		expectedLabels []string
	}{
		{
			name: "names and labels",
			filter: ListConfigsFilter{
				Names: []string{"app-config", "worker-config"},
				Labels: map[string]string{
					"environment": "production",
					"managed":     "true",
				},
			},
			expectedNames:  []string{"app-config", "worker-config"},
			expectedLabels: []string{"environment=production", "managed=true"},
		},
		{
			name: "stack ownership",
			filter: ListConfigsFilter{
				StackName: "payments",
			},
			expectedLabels: []string{stackNamespaceLabelKey + "=payments"},
		},
		{
			name: "stack ownership and explicit label",
			filter: ListConfigsFilter{
				StackName: "payments",
				Labels:    map[string]string{"rotation.managed": "true"},
			},
			expectedLabels: []string{
				"rotation.managed=true",
				stackNamespaceLabelKey + "=payments",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filterArgs := buildConfigListFilters(tt.filter)

			assert.ElementsMatch(t, tt.expectedNames, filterArgs.Get("name"), "unexpected name filters")
			assert.ElementsMatch(t, tt.expectedLabels, filterArgs.Get("label"), "unexpected label filters")
		})
	}
}
