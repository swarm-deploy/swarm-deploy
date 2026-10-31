package deployment

import (
	"errors"
	"testing"
	"time"

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

func TestProjectionIgnoresEventAfterNewerPreparationFailure(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle := deployments.NewService(db, &dispatcher.NopDispatcher{})
	runtime := modelstore.NewSQLStore(db)
	subscriber := NewProjectionSubscriber(db, deploymentstore.NewStore(db), runtime)
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit-a", projectionDesired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, deployments.Completion{Outcome: deployments.CompletionSucceeded}))
	require.NoError(t, runtime.Update(t.Context(), func(state *gitopsmodel.Runtime) {
		state.Stacks["app"] = gitopsmodel.Stack{
			LastCommit: "commit-b", LastError: "preparation_failed",
			StateUpdatedAt: time.Now().UTC().Add(time.Minute),
		}
	}))
	event := events.Envelope{ID: "late", Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{
		DeploymentID: attempt.DeploymentID, AttemptID: attempt.ID, StackName: "app", Commit: "commit-a",
	}}}
	require.NoError(t, subscriber.Handle(t.Context(), event))
	state, err := runtime.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "commit-b", state.Stacks["app"].LastCommit)
	assert.Equal(t, "preparation_failed", state.Stacks["app"].LastError)
}

func TestProjectionSuccessRestoresServicesAndSourceDigest(t *testing.T) {
	db, err := storage.OpenSQLite(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	lifecycle := deployments.NewService(db, &dispatcher.NopDispatcher{})
	runtime := modelstore.NewSQLStore(db)
	subscriber := NewProjectionSubscriber(db, deploymentstore.NewStore(db), runtime)
	desired := projectionDesired("api:1")
	desired.Digest = "source-compose-digest"
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit", desired, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, deployments.Completion{Outcome: deployments.CompletionSucceeded}))
	require.NoError(t, runtime.Update(t.Context(), func(state *gitopsmodel.Runtime) {
		state.Stacks["app"] = gitopsmodel.Stack{
			LastError: "apply_failed",
			Services: map[string]gitopsmodel.Service{"api": {
				Image: "api:1", SyncStatus: gitopsmodel.SyncStatusOutOfSync, SyncError: "apply_failed",
			}},
		}
	}))
	event := events.Envelope{ID: "success", Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{
		DeploymentID: attempt.DeploymentID, AttemptID: attempt.ID, StackName: "app", Commit: "commit",
	}}}
	require.NoError(t, subscriber.Handle(t.Context(), event))
	require.NoError(t, subscriber.Handle(t.Context(), event))
	state, err := runtime.Read(t.Context())
	require.NoError(t, err)
	stack := state.Stacks["app"]
	assert.Equal(t, "source-compose-digest", stack.SourceDigest)
	assert.Empty(t, stack.LastError)
	assert.Equal(t, gitopsmodel.SyncStatusSynced, stack.Services["api"].SyncStatus)
	assert.Empty(t, stack.Services["api"].SyncError)
}
