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
		stack     *bool
		label     string
		wantName  string
		want      bool
		wantError bool
	}{
		{name: "global enabled", global: true, wantName: "global", want: true},
		{name: "global disabled", wantName: "global", want: false},
		{name: "stack disables global", global: true, stack: boolPointer(false), wantName: "stack", want: false},
		{name: "stack enables global", stack: boolPointer(true), wantName: "stack", want: true},
		{name: "service disables global", global: true, label: "false", wantName: "service", want: false},
		{name: "service enables global", label: "true", wantName: "service", want: true},
		{
			name: "service takes precedence over stack", stack: boolPointer(false), label: "true",
			wantName: "service", want: true,
		},
		{name: "invalid service override", label: "sometimes", wantName: "service", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{}
			if tt.label != "" {
				labels[labelsdict.ServiceSyncPolicySelfHealLabelKey] = tt.label
			}
			actualName, actual, err := resolveSelfHeal(compose.Service{
				Deploy: compose.ServiceDeploy{Labels: compose.Labels{Map: labels}},
			}, tt.stack, tt.global)
			assert.Equal(t, tt.wantName, actualName)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func boolPointer(value bool) *bool {
	return &value
}
