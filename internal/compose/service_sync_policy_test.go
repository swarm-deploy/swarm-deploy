package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestServiceSelfHealPolicy(t *testing.T) {
	tests := []struct {
		name      string
		value     *string
		want      bool
		specified bool
		wantError bool
	}{
		{name: "not specified"},
		{name: "enabled", value: stringPointer("yes"), want: true, specified: true},
		{name: "disabled", value: stringPointer("off"), specified: true},
		{name: "invalid", value: stringPointer("maybe"), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{}
			if tt.value != nil {
				labels[labelsdict.ServiceSyncPolicySelfHealLabelKey] = *tt.value
			}
			service := Service{Deploy: ServiceDeploy{Labels: Labels{Map: labels}}}
			actual, specified, err := service.SelfHealPolicy()
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, actual)
			assert.Equal(t, tt.specified, specified)
		})
	}
}

func stringPointer(value string) *string { return &value }
