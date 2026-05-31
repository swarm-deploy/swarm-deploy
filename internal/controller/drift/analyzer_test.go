package drift

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type fakeServiceReader struct {
	status swarm.ServiceStatus
	err    error
}

func (r *fakeServiceReader) GetStatus(context.Context, swarm.ServiceReference) (swarm.ServiceStatus, error) {
	if r.err != nil {
		return swarm.ServiceStatus{}, r.err
	}
	return r.status, nil
}

func TestAnalyzerAnalyzeServiceMissed(t *testing.T) {
	t.Parallel()

	analyzer := NewAnalyzer(&fakeServiceReader{
		err: swarm.ErrServiceNotFound,
	})

	result, err := analyzer.Analyze(context.Background(), "payments", compose.Service{Name: "api"})
	require.NoError(t, err, "analyze drift")

	assert.True(t, result.OutOfSync, "service absence must be out of sync")
	assert.True(t, result.ServiceMissed, "service must be marked as missed")
	assert.False(t, result.Replicas.OutOfSync, "replicas drift must be disabled for missed service")
}

func TestAnalyzerAnalyzeReplicasDrift(t *testing.T) {
	t.Parallel()

	analyzer := NewAnalyzer(&fakeServiceReader{
		status: swarm.ServiceStatus{
			Stack:   "payments",
			Service: "api",
			Spec: swarm.ServiceSpec{
				Replicas: 1,
			},
		},
	})

	desiredReplicas := uint64(3)
	result, err := analyzer.Analyze(context.Background(), "payments", compose.Service{
		Name:     "api",
		Replicas: &desiredReplicas,
	})
	require.NoError(t, err, "analyze drift")

	assert.True(t, result.OutOfSync, "replicas mismatch must be out of sync")
	assert.False(t, result.ServiceMissed, "service exists in runtime")
	assert.True(t, result.Replicas.OutOfSync, "replicas must be marked as drifted")
	assert.Equal(t, uint(3), result.Replicas.Desired, "unexpected desired replicas")
	assert.Equal(t, uint(1), result.Replicas.Live, "unexpected live replicas")
}
