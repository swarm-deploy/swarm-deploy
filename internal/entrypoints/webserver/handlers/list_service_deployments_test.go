package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestListServiceDeploymentsReadsDeploymentStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	publisher := eventbus.NewOutboxDispatcher(outbox.New(db))
	svc := deployments.NewService(db, publisher)
	h := &handler{deployments: deploymentstore.NewStore(db)}

	file := func(services ...compose.Service) compose.File {
		return compose.File{Compose: compose.Compose{Services: services}}
	}
	start := func(stack, commit string, services ...compose.Service) *deploymentmodel.Attempt {
		attempt, startErr := svc.StartIfChanged(ctx, stack, commit, file(services...), true, deployments.StartOptions{TargetServices: file(services...).Compose.Services.Names()})
		require.NoError(t, startErr)
		require.NotNil(t, attempt)
		return attempt
	}

	first := start("payments", "sha-1", compose.Service{Name: "api", Image: "registry.example.com/api:v1"})
	require.NoError(t, svc.Complete(ctx, first.ID, deployments.Completion{Outcome: deployments.CompletionApplyFailed}))
	worker := start("payments", "worker-only", compose.Service{Name: "worker", Image: "worker:v1"})
	require.NoError(t, svc.Complete(ctx, worker.ID, deployments.Completion{Outcome: deployments.CompletionApplyFailed}))
	changed := start("payments", "sha-2", compose.Service{Name: "api", Image: "registry.example.com/api:v2"})
	require.NoError(t, svc.Complete(ctx, changed.ID, deployments.Completion{Outcome: deployments.CompletionApplyFailed}))
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

func TestListServiceDeploymentsFollowsAttemptTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc := deployments.NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	h := &handler{deployments: deploymentstore.NewStore(db)}
	file := func(worker string) compose.File {
		return compose.File{Compose: compose.Compose{Services: compose.Services{
			{Name: "api", Image: "api:v1"}, {Name: "worker", Image: worker},
		}}}
	}
	history := func(service string) []generated.ServiceDeploymentResponse {
		response, listErr := h.ListServiceDeployments(ctx, generated.ListServiceDeploymentsParams{Stack: "app", Service: service})
		require.NoError(t, listErr)
		return response.Deployments
	}

	// Historical whole-stack Attempt: null targets include every service.
	initial, err := svc.StartIfChanged(ctx, "app", "sha-1", file("worker:v1"), true)
	require.NoError(t, err)
	require.NoError(t, svc.Complete(ctx, initial.ID, deployments.Completion{Outcome: deployments.CompletionSucceeded}))
	_, err = db.Exec(ctx, "UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.target_services', '$.service_revisions')")
	require.NoError(t, err)

	// A failed update of worker only.
	failed, err := svc.StartIfChanged(ctx, "app", "sha-2", file("worker:v2"), true,
		deployments.StartOptions{TargetServices: []string{"worker"}, ServiceCommits: map[string]string{"worker": "worker-commit"}})
	require.NoError(t, err)
	require.NoError(t, svc.Complete(ctx, failed.ID, deployments.Completion{Outcome: deployments.CompletionApplyFailed}))

	api := history("api")
	require.Len(t, api, 1, "the unchanged api did not take part in the worker update")
	assert.Equal(t, initial.DeploymentID, api[0].ID)
	assert.Equal(t, generated.ServiceDeploymentStatusSucceeded, api[0].Status)
	worker := history("worker")
	require.Len(t, worker, 2)
	assert.Equal(t, failed.DeploymentID, worker[0].ID)
	assert.Equal(t, generated.ServiceDeploymentStatusFailed, worker[0].Status)
	assert.Equal(t, "worker-commit", worker[0].Commit.Value)

	// A later Attempt of the same desired state for api keeps the earlier failed worker Attempt.
	retry, err := svc.StartIfChanged(ctx, "app", "sha-2", file("worker:v2"), false,
		deployments.StartOptions{TargetServices: []string{"api"}, ManualRetry: true})
	require.NoError(t, err)
	require.NotNil(t, retry)
	require.NoError(t, svc.Complete(ctx, retry.ID, deployments.Completion{Outcome: deployments.CompletionSucceeded}))
	assert.Equal(t, generated.ServiceDeploymentStatusFailed, history("worker")[0].Status)
	assert.Equal(t, generated.ServiceDeploymentStatusSucceeded, history("api")[0].Status)

	// Deployments recorded before Attempts were persisted remain whole-stack.
	_, err = db.Exec(ctx, "DELETE FROM deployment_attempts WHERE deployment_id = ?", initial.DeploymentID)
	require.NoError(t, err)
	// Migration 0005 indexes such deployments without an Attempt.
	_, err = db.Exec(ctx, `INSERT INTO deployment_services (deployment_id, attempt_id, service_name)
		VALUES (?, NULL, 'api'), (?, NULL, 'worker')`, initial.DeploymentID, initial.DeploymentID)
	require.NoError(t, err)
	assert.Len(t, history("api"), 2)
}

func TestListServiceDeploymentsWithoutAttemptsIsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	h := &handler{deployments: deploymentstore.NewStore(db)}
	response, err := h.ListServiceDeployments(ctx, generated.ListServiceDeploymentsParams{
		Stack: "payments", Service: "api",
	})
	require.NoError(t, err)
	assert.Empty(t, response.Deployments)
}
