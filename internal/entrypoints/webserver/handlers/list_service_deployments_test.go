package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestListServiceDeploymentsReadsDeploymentStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc := deployment.NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	h := &handler{deployments: deploymentstore.NewStore(db)}

	file := func(services ...compose.Service) compose.File {
		return compose.File{Compose: compose.Compose{Services: services}}
	}
	start := func(stack, commit string, services ...compose.Service) *deploymentmodel.Attempt {
		attempt, startErr := svc.StartIfChanged(ctx, stack, commit, file(services...), true)
		require.NoError(t, startErr)
		require.NotNil(t, attempt)
		return attempt
	}

	first := start("payments", "sha-1", compose.Service{Name: "api", Image: "registry.example.com/api:v1"})
	require.NoError(t, svc.Fail(ctx, first.ID, gitopsmodel.Stack{}, "apply_failed"))
	worker := start("payments", "worker-only", compose.Service{Name: "worker", Image: "worker:v1"})
	require.NoError(t, svc.Fail(ctx, worker.ID, gitopsmodel.Stack{}, "apply_failed"))
	changed := start("payments", "sha-2", compose.Service{Name: "api", Image: "registry.example.com/api:v2"})
	require.NoError(t, svc.Fail(ctx, changed.ID, gitopsmodel.Stack{}, "apply_failed"))
	replicas := uint64(2)
	unchanged := start("payments", "sha-3", compose.Service{
		Name: "api", Image: "registry.example.com/api:v2", Deploy: compose.ServiceDeploy{Replicas: &replicas},
	})
	start("other-stack", "unrelated", compose.Service{Name: "api", Image: "other:v1"})

	response, err := h.ListServiceDeployments(ctx, generated.ListServiceDeploymentsParams{
		Stack: "payments", Service: "api", Limit: generated.NewOptInt32(2),
	})
	require.NoError(t, err)
	require.Len(t, response.Deployments, 2)
	assert.Equal(t, unchanged.DeploymentID, response.Deployments[0].ID)
	assert.Equal(t, generated.ServiceDeploymentStatusRunning, response.Deployments[0].Status)
	assert.Equal(t, unchanged.StartedAt, response.Deployments[0].CreatedAt)
	assert.Equal(t, "sha-3", response.Deployments[0].Commit.Value)
	assert.Equal(t, "registry.example.com/api:v2", response.Deployments[0].Image)
	assert.Equal(t, "v2", response.Deployments[0].ImageVersion)
	assert.Equal(t, changed.DeploymentID, response.Deployments[1].ID)
	assert.Equal(t, generated.ServiceDeploymentStatusSuperseded, response.Deployments[1].Status)
	assert.Equal(t, "v2", response.Deployments[1].ImageVersion)

	response, err = h.ListServiceDeployments(ctx, generated.ListServiceDeploymentsParams{
		Stack: "payments", Service: "api",
	})
	require.NoError(t, err)
	require.Len(t, response.Deployments, 3)
	assert.Equal(t, first.DeploymentID, response.Deployments[2].ID)
	assert.Equal(t, "v1", response.Deployments[2].ImageVersion)
}

func TestListServiceDeploymentsWithoutAttemptsIsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	h := &handler{deployments: deploymentstore.NewStore(db)}
	response, err := h.ListServiceDeployments(ctx, generated.ListServiceDeploymentsParams{
		Stack: "payments", Service: "api",
	})
	require.NoError(t, err)
	assert.Empty(t, response.Deployments)
}
