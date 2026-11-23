package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildSecretListFilters(t *testing.T) {
	tests := []struct {
		name           string
		filter         ListSecretsFilter
		expectedNames  []string
		expectedLabels []string
	}{
		{
			name: "names and labels",
			filter: ListSecretsFilter{
				Names: []string{"app-secret", "worker-secret"},
				Labels: map[string]string{
					"environment": "production",
					"managed":     "true",
				},
			},
			expectedNames:  []string{"app-secret", "worker-secret"},
			expectedLabels: []string{"environment=production", "managed=true"},
		},
		{
			name: "stack ownership",
			filter: ListSecretsFilter{
				StackName: "payments",
			},
			expectedLabels: []string{stackNamespaceLabelKey + "=payments"},
		},
		{
			name: "stack ownership and explicit label",
			filter: ListSecretsFilter{
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
			filterArgs := buildSecretListFilters(tt.filter)

			assert.ElementsMatch(t, tt.expectedNames, filterArgs.Get("name"), "unexpected name filters")
			assert.ElementsMatch(t, tt.expectedLabels, filterArgs.Get("label"), "unexpected label filters")
		})
	}
}
