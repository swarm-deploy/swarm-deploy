package drift

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
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
				ServiceName: "api", Reason: "Service Missed", ServiceMissed: true,
			}},
		},
		{
			name:    "replicas diverged",
			desired: compose.Service{Name: "api", Deploy: compose.ServiceDeploy{Replicas: &three}},
			live:    []swarm.StackService{{Name: "api", Replicas: &one}},
			want: map[string]ServiceDrift{"api": {
				ServiceName: "api", Reason: "Service Replicas Diverged",
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
