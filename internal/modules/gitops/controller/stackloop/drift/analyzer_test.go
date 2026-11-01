package drift

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestAnalyzerAnalyze(t *testing.T) {
	one := uint64(1)
	three := uint64(3)

	tests := []struct {
		name    string
		desired compose.Service
		live    []swarm.StackService
		want    map[string]ServiceDrift
	}{
		{
			name:    "missing service",
			desired: compose.Service{Name: "api"},
			want: map[string]ServiceDrift{"api": {
				ServiceName: "api", Reason: "service is missing", ServiceMissed: true,
			}},
		},
		{
			name:    "replicas diverged",
			desired: compose.Service{Name: "api", Deploy: compose.ServiceDeploy{Replicas: &three}},
			live:    []swarm.StackService{{Name: "api", Replicas: &one}},
			want: map[string]ServiceDrift{"api": {
				ServiceName: "api", Reason: "replica count differs: desired=3 actual=1",
				Replicas: ReplicasDrift{OutOfSync: true, Desired: 3, Live: 1},
			}},
		},
		{
			name:    "replicas match",
			desired: compose.Service{Name: "api", Deploy: compose.ServiceDeploy{Replicas: &three}},
			live:    []swarm.StackService{{Name: "api", Replicas: &three}},
			want:    map[string]ServiceDrift{},
		},
		{
			name:    "replicas unspecified",
			desired: compose.Service{Name: "api"},
			live:    []swarm.StackService{{Name: "api", Replicas: &one}},
			want:    map[string]ServiceDrift{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := NewAnalyzer()
			response, err := analyzer.Analyze(AnalyzeRequest{
				Desired: compose.File{Compose: compose.Compose{Services: compose.Services{tt.desired}}},
				Live:    tt.live,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, response.Drifts)
		})
	}
}

func TestAnalyzerReplicaDriftPolicy(t *testing.T) {
	one, three := uint64(1), uint64(3)
	tests := []struct {
		name      string
		policy    string
		managed   string
		selfHeal  string
		missing   bool
		wantDrift bool
		wantErr   bool
	}{
		{name: "default detects replicas", wantDrift: true},
		{name: "explicit enabled", policy: "true", wantDrift: true},
		{name: "explicit disabled", policy: "false"},
		{name: "disabled boolean alias", policy: "off"},
		{name: "missing still detected", policy: "false", missing: true, wantDrift: true},
		{name: "self heal disabled still detects", selfHeal: "false", wantDrift: true},
		{name: "external replicas", managed: "false"},
		{name: "external overrides enabled drift", managed: "off", policy: "true"},
		{name: "managed replicas default drift", managed: "true", wantDrift: true},
		{name: "managed replicas disabled drift", managed: "true", policy: "false"},
		{name: "external missing detected", managed: "false", missing: true, wantDrift: true},
		{name: "invalid ownership policy", managed: "maybe", wantErr: true},
		{name: "invalid replica policy", policy: "sometimes", wantErr: true},
		{name: "invalid replica policy on missing service", policy: "sometimes", missing: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{}
			if tt.policy != "" {
				labels[labelsdict.ServiceSyncPolicySelfHealReplicasLabelKey] = tt.policy
			}
			if tt.managed != "" {
				labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = tt.managed
			}
			if tt.selfHeal != "" {
				labels[labelsdict.ServiceSyncPolicySelfHealLabelKey] = tt.selfHeal
			}
			live := []swarm.StackService{{Name: "worker", Replicas: &one}}
			if tt.missing {
				live = nil
			}
			resp, err := NewAnalyzer().Analyze(AnalyzeRequest{
				Desired: compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "worker", Deploy: compose.ServiceDeploy{
					Replicas: &three, Labels: *compose.NewLabels(labels),
				}}}}}, Live: live,
			})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if !tt.wantDrift {
				assert.Empty(t, resp.Drifts)
				return
			}
			require.Contains(t, resp.Drifts, "worker")
			assert.Equal(t, tt.missing, resp.Drifts["worker"].ServiceMissed)
			assert.Equal(t, !tt.missing, resp.Drifts["worker"].Replicas.OutOfSync)
		})
	}
}
