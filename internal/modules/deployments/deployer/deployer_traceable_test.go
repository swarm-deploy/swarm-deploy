package deployer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestTraceableDeployerPreservesResultAndError(t *testing.T) {
	ctrl := gomock.NewController(t)
	inner := NewMockStackDeployer(ctrl)
	deployErr := errors.New("deploy failed")
	now := time.Now().UTC()
	want := Result{InitJobs: []InitJobResult{{
		ServiceName: "api", JobName: "migrate", Status: InitJobSucceeded,
		StartedAt: now, FinishedAt: now, Logs: []byte("done"), LogsAvailable: true,
	}}}
	inner.EXPECT().DeployStack(gomock.Any(), "app", "source.yaml", "deploy.yaml", gomock.Any()).Return(want, deployErr)
	wrapped := newTraceableDeployer(trace.NewNoopTracerProvider(), inner)

	result, err := wrapped.DeployStack(context.Background(), "app", "source.yaml", "deploy.yaml", compose.Compose{})

	require.ErrorIs(t, err, deployErr)
	assert.Equal(t, want, result)
}
