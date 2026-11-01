package compose

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestServiceReplicaPolicies(t *testing.T) {
	tests := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{value: "true", want: true}, {value: "1", want: true}, {value: "yes", want: true},
		{value: "y", want: true}, {value: "on", want: true}, {value: " TRUE ", want: true},
		{value: "false"}, {value: "0"}, {value: "no"}, {value: "n"}, {value: "off"}, {value: " OFF "},
		{value: "maybe", wantErr: true}, {value: "", wantErr: true},
	}
	for _, policy := range []struct {
		label string
		parse func(Service) (bool, bool, error)
	}{
		{labelsdict.ServiceSyncPolicySelfHealReplicasLabelKey, Service.ReplicaDriftPolicy},
		{labelsdict.ServiceSyncPolicyReplicasManagedLabelKey, Service.ReplicasManagedPolicy},
	} {
		t.Run(policy.label, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.value, func(t *testing.T) {
					service := Service{Deploy: ServiceDeploy{Labels: *NewLabels(map[string]string{policy.label: tt.value})}}
					got, specified, err := policy.parse(service)
					assert.True(t, specified)
					if tt.wantErr {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					assert.Equal(t, tt.want, got)
				})
			}
			_, specified, err := policy.parse(Service{})
			require.NoError(t, err)
			assert.False(t, specified)
		})
	}
}

func TestLoaderValidatesServiceSyncPolicies(t *testing.T) {
	for _, label := range []string{labelsdict.ServiceSyncPolicySelfHealLabelKey, labelsdict.ServiceSyncPolicySelfHealReplicasLabelKey, labelsdict.ServiceSyncPolicyReplicasManagedLabelKey} {
		t.Run(label, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compose.yaml")
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`services:
  worker:
    image: example/job:1.0.0
    deploy:
      labels:
        %s: "maybe"
`, label)), 0o600))
			_, err := NewFileLoader().Load(t.Context(), path)
			var validation *ValidateComposeError
			require.ErrorAs(t, err, &validation)
			require.Len(t, validation.Issues, 1)
			assert.Equal(t, "worker", validation.Issues[0].ResourceName)
			assert.Equal(t, "deploy.labels."+label, validation.Issues[0].Field)
			assert.Equal(t, IssueCodeInvalidValue, validation.Issues[0].Code)
		})
	}
}
