package stackloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestResolveSelfHeal(t *testing.T) {
	tests := []struct {
		name      string
		global    bool
		label     string
		want      bool
		wantError bool
	}{
		{name: "global enabled", global: true, want: true},
		{name: "global disabled", want: false},
		{name: "service disables global", global: true, label: "false", want: false},
		{name: "service enables global", label: "true", want: true},
		{name: "invalid service override", label: "sometimes", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{}
			if tt.label != "" {
				labels[labelsdict.ServiceSyncPolicySelfHealLabelKey] = tt.label
			}
			actual, err := resolveSelfHeal(compose.Service{
				Deploy: compose.ServiceDeploy{Labels: compose.Labels{Map: labels}},
			}, tt.global)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, actual)
		})
	}
}
