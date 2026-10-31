package deployment

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestProjectionSubscriberIsIdempotentAndRejectsStaleAttempts(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle := deployments.NewService(db, &dispatcher.NopDispatcher{})
	store := deploymentstore.NewStore(db)
	runtime := modelstore.NewSQLStore(db)
	subscriber := NewProjectionSubscriber(db, store, runtime)

	first, err := lifecycle.StartIfChanged(t.Context(), "app", "first", projectionDesired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, deployments.Completion{
		Outcome: deployments.CompletionApplyFailed, Diagnostic: errors.New("apply failed"),
	}))
	retry, err := lifecycle.StartIfChanged(t.Context(), "app", "retry", projectionDesired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), retry.ID, deployments.Completion{
		Outcome: deployments.CompletionSucceeded,
	}))

	success := events.Envelope{ID: "success", Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{
		DeploymentID: retry.DeploymentID, AttemptID: retry.ID, StackName: "app", Commit: "retry",
	}}}
	require.NoError(t, subscriber.Handle(t.Context(), success))
	require.NoError(t, subscriber.Handle(t.Context(), success))
	stale := events.Envelope{ID: "stale", Event: &events.DeployFailed{DeployEvent: events.DeployEvent{
		DeploymentID: first.DeploymentID, AttemptID: first.ID, StackName: "app", Commit: "first",
	}, ErrorCode: "apply_failed"}}
	require.NoError(t, subscriber.Handle(t.Context(), stale))

	state, err := runtime.Read(t.Context())
	require.NoError(t, err)
	projected := state.Stacks["app"]
	assert.Equal(t, "first", projected.LastCommit)
	assert.Empty(t, projected.LastError)
	assert.Equal(t, gitopsmodel.SyncStatus(gitopsmodel.SyncStatusSynced), projected.Services["api"].SyncStatus)
	assert.Equal(t, deploymentmodel.Succeeded, mustDeployment(t, store, retry.DeploymentID).Status)
}

func projectionDesired(image string) compose.File {
	return compose.File{Path: "app.yaml", Digest: image, Compose: compose.Compose{
		Services: compose.Services{{Name: "api", Image: image}},
	}}
}

func mustDeployment(t *testing.T, store *deploymentstore.Store, id string) deploymentmodel.Deployment {
	t.Helper()
	deployment, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	return deployment
}
