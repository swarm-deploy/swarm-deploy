package preparation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestAddManagedLabel(t *testing.T) {
	tests := []struct {
		name            string
		labels          compose.Labels
		expectedMutated bool
	}{
		{
			name:            "labels absent",
			expectedMutated: true,
		},
		{
			name:            "managed label absent",
			labels:          *compose.NewLabels(map[string]string{"purpose": "api"}),
			expectedMutated: true,
		},
		{
			name: "managed label has incorrect value",
			labels: *compose.NewLabels(map[string]string{
				labelsdict.ServiceManagedLabelKey: "false",
			}),
			expectedMutated: true,
		},
		{
			name: "managed label already correct",
			labels: *compose.NewLabels(map[string]string{
				labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
			}),
			expectedMutated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := &compose.File{Compose: compose.Compose{
				Services: compose.Services{{Name: "api", Deploy: compose.ServiceDeploy{Labels: tt.labels}}},
			}}

			assert.Equal(t, tt.expectedMutated, addManagedLabel(desired))
			assert.Equal(
				t,
				labelsdict.ServiceManagedLabelValue,
				desired.Compose.Services[0].Deploy.Labels.Map[labelsdict.ServiceManagedLabelKey],
			)
			if tt.labels.Map["purpose"] != "" {
				assert.Equal(t, "api", desired.Compose.Services[0].Deploy.Labels.Map["purpose"])
			}
		})
	}
}
