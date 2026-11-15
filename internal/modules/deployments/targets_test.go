package deployments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func TestTargetedAttemptsAccumulateSuccessForSameDesired(t *testing.T) {
	db, service, bus := openService(t)
	subscribe(t, db, bus, events.TypeNameDeploySuccess)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api", "worker")
	first, err := service.StartIfChanged(t.Context(), "app", "stack-commit", desired, true, StartOptions{
		TargetServices: []string{"api"}, ServiceCommits: map[string]string{"api": "api-commit"},
	})
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	// Recovery may repair a stale running attempt from its terminal deployment.
	_, err = db.Exec(t.Context(), "UPDATE deployment_attempts SET status = 'running', finished_at = NULL WHERE id = ?", first.ID)
	require.NoError(t, err)
	require.NoError(t, NewService(db, bus).InterruptRunning(t.Context()))
	persisted, err := store.GetAttempt(t.Context(), first.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, persisted.TargetServices)
	assert.Equal(t, "api-commit", persisted.ServiceRevisions["api"].Commit)
	assert.NotEmpty(t, persisted.ServiceRevisions["api"].Digest)
	_, err = store.Baseline(t.Context(), "app")
	require.ErrorIs(t, err, deploymentstore.ErrNotFound, "unapplied worker cannot become the stack baseline")
	duplicate, err := service.StartIfChanged(t.Context(), "app", "later", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	assert.Nil(t, duplicate)
	// Reconstruct lifecycle to exercise persisted receipts rather than in-memory state.
	service = NewService(db, bus)
	second, err := service.StartIfChanged(t.Context(), "app", "later", desired, false, StartOptions{
		TargetServices: []string{"worker"}, ServiceCommits: map[string]string{"worker": "worker-commit"},
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.DeploymentID, second.DeploymentID)
	assert.Equal(t, 2, second.Number)
	require.NoError(t, service.Complete(t.Context(), second.ID, Completion{Outcome: CompletionSucceeded}))
	baseline, err := store.Baseline(t.Context(), "app")
	require.NoError(t, err)
	assert.Len(t, baseline.ServiceDigests, 2)
	assert.Len(t, publications(t, db, events.TypeNameDeploySuccess), 2)
}

func TestTargetedCompletionKeepsUnselectedVerificationAndBaseline(t *testing.T) {
	db, service, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api", "worker")
	first, err := service.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: desired.Compose.Services.Names()})
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	desired.Compose.Services[0].Image = "api:2"
	desired.Compose.Services[1].Image = "worker:2"
	failed, err := service.StartIfChanged(t.Context(), "app", "two", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), failed.ID, Completion{Outcome: CompletionProcessInterrupted}))
	next, err := service.StartIfChanged(t.Context(), "app", "two", desired, false, StartOptions{TargetServices: []string{"worker"}, ManualRetry: true})
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), next.ID, Completion{Outcome: CompletionSucceeded}))
	current, err := store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, current.Verification.UnresolvedServices)
	baselineID, err := store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.DeploymentID, baselineID)
}

func TestEmptyAndInvalidTargetScopes(t *testing.T) {
	db, service, _ := openService(t)
	desired := desiredServices("api")
	attempt, err := service.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{}})
	require.NoError(t, err)
	require.NotNil(t, attempt.TargetServices)
	require.NoError(t, service.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded}))
	stored, err := deploymentstore.NewStore(db).GetAttempt(t.Context(), attempt.ID)
	require.NoError(t, err)
	assert.False(t, stored.Targets("api"))
	_, err = deploymentstore.NewStore(db).Baseline(t.Context(), "app")
	require.ErrorIs(t, err, deploymentstore.ErrNotFound)
	_, err = service.StartIfChanged(t.Context(), "other", "one", desired, true, StartOptions{TargetServices: []string{"missing"}})
	require.ErrorContains(t, err, "absent from desired")
	assert.True(t, (deploymentmodel.Attempt{}).Targets("api"), "historical attempts retain whole-stack scope")
}

func TestNilTargetScopeSelectsWholeStack(t *testing.T) {
	db, service, _ := openService(t)
	desired := desiredServices("api", "worker")
	attempt, err := service.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{})
	require.NoError(t, err)
	require.NotNil(t, attempt)
	assert.Nil(t, attempt.TargetServices)
	assert.True(t, attempt.Targets("api") && attempt.Targets("worker"))
	assert.Len(t, attempt.ServiceRevisions, 2, "whole-stack attempts keep receipts for every service")
	require.NoError(t, service.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded}))
	stored, err := deploymentstore.NewStore(db).GetAttempt(t.Context(), attempt.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.TargetServices)
	_, err = deploymentstore.NewStore(db).Baseline(t.Context(), "app")
	require.NoError(t, err)
}

func TestServiceRevisionChangeCreatesFreshDesiredSnapshot(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api")
	desired.Compose.Secrets = compose.Secrets{"a": {External: true}, "b": {External: true}}
	desired.Compose.Services[0].Secrets = []compose.ObjectRef{{Source: "a"}, {Source: "b"}}
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	desired.Compose.Services[0].Secrets = []compose.ObjectRef{{Source: "b"}, {Source: "a"}}
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.NotEqual(t, first.DeploymentID, next.DeploymentID)
	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionSucceeded}))
	baseline, err := store.Baseline(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, next.ServiceRevisions["api"].Digest, baseline.ServiceDigests["api"])
}

func TestLegacySnapshotAdoptsServiceDigestsWithoutRedeploy(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api", "worker")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	// Simulate a snapshot written before per-service digests existed.
	_, err = db.Exec(t.Context(), "UPDATE deployments SET desired_payload = json_remove(desired_payload, '$.service_digests') WHERE id = ?", first.DeploymentID)
	require.NoError(t, err)

	next, err := lifecycle.StartIfChanged(t.Context(), "app", "upgrade", desired, false, StartOptions{TargetServices: []string{}})
	require.NoError(t, err)
	assert.Nil(t, next, "an upgrade must not redeploy an unchanged stack")
	baseline, err := store.Baseline(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.ServiceRevisions["api"].Digest, baseline.ServiceDigests["api"])
	assert.Equal(t, first.ServiceRevisions["worker"].Digest, baseline.ServiceDigests["worker"])

	// A later change of one service is covered by the adopted baseline for the other.
	desired = desiredServices("api", "worker")
	desired.Compose.Services[0].Image = "api:2"
	changed, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, changed)
	require.NoError(t, lifecycle.Complete(t.Context(), changed.ID, Completion{Outcome: CompletionSucceeded}))
	baselineID, err := store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, changed.DeploymentID, baselineID)
}

func TestFailedTopUpKeepsBaselineAndResetsRetryBackoff(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	// Drop the receipt so the same desired state receives another targeted Attempt.
	_, err = db.Exec(t.Context(), "UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.service_revisions') WHERE id = ?", first.ID)
	require.NoError(t, err)
	topUp, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, topUp)
	assert.Equal(t, first.DeploymentID, topUp.DeploymentID)

	current, err := store.Get(t.Context(), topUp.DeploymentID)
	require.NoError(t, err)
	assert.False(t, retriesFailedAttempt(current, *topUp), "a top-up after success is not a retry")

	require.NoError(t, lifecycle.Complete(t.Context(), topUp.ID, Completion{Outcome: CompletionProcessInterrupted}))
	_, err = store.Baseline(t.Context(), "app")
	require.NoError(t, err, "a failed top-up must not hide the successful baseline")
	current, err = store.Get(t.Context(), topUp.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, 1, consecutiveFailures(current), "the earlier success resets retry backoff")
}
