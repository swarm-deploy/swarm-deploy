package drift

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestAnalyzerDetectsMissingAndReplicaDrift(t *testing.T) {
	desiredReplicas := uint64(3)
	liveReplicas := uint64(2)

	analyzer := NewAnalyzer()
	response, err := analyzer.Analyze(AnalyzeRequest{
		Desired: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{Name: "api", Deploy: compose.ServiceDeploy{Replicas: &desiredReplicas}},
					{Name: "worker"},
				},
			},
		},
		Live: []swarm.StackService{
			{Name: "api", Replicas: &liveReplicas},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, ServiceDrift{
		ServiceName:             "api",
		Reason:                  "Service Replicas Diverged",
		ServiceReplicasDiverged: true,
	}, response.Drifts["api"])
	assert.Equal(t, ServiceDrift{
		ServiceName:   "worker",
		Reason:        "Service Missed",
		ServiceMissed: true,
	}, response.Drifts["worker"])
}

func TestAnalyzerIgnoresMatchingReplicas(t *testing.T) {
	replicas := uint64(2)

	response, err := NewAnalyzer().Analyze(AnalyzeRequest{
		Desired: compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{Name: "api", Deploy: compose.ServiceDeploy{Replicas: &replicas}},
				},
			},
		},
		Live: []swarm.StackService{
			{Name: "api", Replicas: &replicas},
		},
	})

	require.NoError(t, err)
	assert.Empty(t, response.Drifts)
}
