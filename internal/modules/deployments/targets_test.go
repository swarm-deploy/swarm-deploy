package deployments

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

func TestTargetedDeploymentsAccumulateSuccessForSameDesired(t *testing.T) {
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
	assert.NotEqual(t, first.DeploymentID, second.DeploymentID)
	assert.Equal(t, 1, second.Number)
	assert.Equal(t, []string{"worker"}, second.TargetServices)
	original, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, original.Status)
	assert.Equal(t, []deploymentmodel.Attempt{persisted}, original.Attempts)
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

	recovery, err := service.StartIfChanged(t.Context(), "app", "two", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, recovery)
	assert.NotEqual(t, next.DeploymentID, recovery.DeploymentID)
	inherited, err := store.Get(t.Context(), recovery.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, current.Verification, inherited.Verification)
	require.NoError(t, service.Complete(t.Context(), recovery.ID, Completion{Outcome: CompletionSucceeded}))
	baselineID, err = store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, recovery.DeploymentID, baselineID)
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
	for name, legacyReceipts := range map[string]string{
		"attempt without receipts":    "UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.service_revisions', '$.target_services')",
		"deployment without attempts": "DELETE FROM deployment_attempts",
	} {
		t.Run(name, func(t *testing.T) {
			db, lifecycle, _ := openService(t)
			store := deploymentstore.NewStore(db)
			desired := desiredServices("api", "worker")
			first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{})
			require.NoError(t, err)
			require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
			// Simulate a deployment recorded before per-service digests and receipts existed.
			_, err = db.Exec(t.Context(), "UPDATE deployments SET desired_payload = json_remove(desired_payload, '$.service_digests')")
			require.NoError(t, err)
			_, err = db.Exec(t.Context(), legacyReceipts)
			require.NoError(t, err)

			// GitOps has no applied revisions for legacy records and selects every service.
			next, err := lifecycle.StartIfChanged(t.Context(), "app", "upgrade", desired, false,
				StartOptions{TargetServices: []string{"api", "worker"}})
			require.NoError(t, err)
			assert.Nil(t, next, "an upgrade must not redeploy an unchanged effective state")
			latest, snapshot, err := store.Latest(t.Context(), "app")
			require.NoError(t, err)
			revisions, err := lifecycle.appliedRevisions(t.Context(), latest, snapshot, nil)
			require.NoError(t, err)
			assert.Equal(t, first.ServiceRevisions["api"].Digest, revisions["api"].Digest)
			assert.Equal(t, "one", revisions["worker"].Commit, "the deployed stack commit becomes the service commit")

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
		})
	}
}

func TestLegacySnapshotDeploysServicesWithUnconfirmedMountOrder(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api", "worker")
	desired.Compose.Secrets = compose.Secrets{"a": {External: true}, "b": {External: true}}
	desired.Compose.Services[0].Secrets = []compose.ObjectRef{{Source: "a"}, {Source: "b"}}
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	_, err = db.Exec(t.Context(), "UPDATE deployments SET desired_payload = json_remove(desired_payload, '$.service_digests')")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.service_revisions')")
	require.NoError(t, err)

	// Reordered before the first run of the new version: the whole-stack digest is unchanged.
	desired.Compose.Services[0].Secrets = []compose.ObjectRef{{Source: "b"}, {Source: "a"}}
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false,
		StartOptions{TargetServices: []string{"api", "worker"}})
	require.NoError(t, err)
	require.NotNil(t, next, "the applied mount order of api is unknown")
	assert.NotEqual(t, first.DeploymentID, next.DeploymentID)
	assert.Equal(t, 1, next.Number)
	assert.Equal(t, []string{"api"}, next.TargetServices, "worker is confirmed by the historical snapshot")
	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionApplyFailed, Diagnostic: assert.AnError}))

	original, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, original.Status)
	require.Len(t, original.Attempts, 1)
	assert.Equal(t, first.ID, original.Attempts[0].ID)
	assert.Equal(t, deploymentmodel.Succeeded, original.Attempts[0].Status)

	// GitOps has not recorded worker as applied yet and selects both services again.
	retry, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false,
		StartOptions{TargetServices: []string{"api", "worker"}, ManualRetry: true})
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, next.DeploymentID, retry.DeploymentID)
	assert.Equal(t, 2, retry.Number)
	assert.Equal(t, []string{"api"}, retry.TargetServices, "a retry does not repeat the confirmed worker")
	require.NoError(t, lifecycle.Complete(t.Context(), retry.ID, Completion{Outcome: CompletionSucceeded}))

	snapshot, err := store.Snapshot(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, snapshot.UnconfirmedServices, "the old snapshot cannot attest to mount order")
	snapshot, err = store.Snapshot(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Empty(t, snapshot.UnconfirmedServices)
	baselineID, err := store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, next.DeploymentID, baselineID)
	again, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false,
		StartOptions{TargetServices: []string{"api", "worker"}})
	require.NoError(t, err)
	assert.Nil(t, again)
}

func TestRetryWithoutUnappliedTargetsStillRetriesCleanup(t *testing.T) {
	_, lifecycle, _ := openService(t)
	desired := desiredServices("api")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{}})
	require.NoError(t, err)
	observedAt := time.Now()
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{
		Outcome: CompletionCleanupFailed, Diagnostic: assert.AnError, ObservedAt: &observedAt,
	}))

	retry, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false,
		StartOptions{TargetServices: []string{}, ManualRetry: true})
	require.NoError(t, err)
	require.NotNil(t, retry, "an empty scope retries cleanup")
	assert.Equal(t, first.DeploymentID, retry.DeploymentID)
	assert.NotNil(t, retry.TargetServices)
	assert.Empty(t, retry.TargetServices)
}

func TestLegacySnapshotWithChangedEffectiveStateRedeploys(t *testing.T) {
	db, lifecycle, _ := openService(t)
	desired := desiredServices("api")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	_, err = db.Exec(t.Context(), "UPDATE deployments SET desired_payload = json_remove(desired_payload, '$.service_digests')")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "DELETE FROM deployment_attempts")
	require.NoError(t, err)

	// Same source, different prepared service: e.g. injected variables enabled during the upgrade.
	desired.Compose.Services[0].Environment = compose.Environment{Map: map[string]string{"INJECTED": "1"}}
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, next, "a changed effective state must be deployed")
	assert.NotEqual(t, first.DeploymentID, next.DeploymentID)
}

func TestNilTargetScopeIncludesUnappliedServices(t *testing.T) {
	_, lifecycle, _ := openService(t)
	desired := desiredServices("api", "worker")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))

	next, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false, StartOptions{})
	require.NoError(t, err)
	require.NotNil(t, next, "a nil scope must not skip the unapplied worker")
	assert.NotEqual(t, first.DeploymentID, next.DeploymentID)
	assert.Equal(t, []string{"worker"}, next.TargetServices, "applied targets are not executed again")
	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionSucceeded}))

	again, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false, StartOptions{})
	require.NoError(t, err)
	assert.Nil(t, again, "every service is applied")
}

func TestSameSnapshotWithoutReceiptCreatesDeploymentAfterSuccess(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	// The runtime selects api again but the prior successful deployment lacks its receipt.
	_, err = db.Exec(t.Context(), "UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.service_revisions') WHERE id = ?", first.ID)
	require.NoError(t, err)
	next, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, false, StartOptions{TargetServices: []string{"api"}})
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.NotEqual(t, first.DeploymentID, next.DeploymentID)
	assert.Equal(t, 1, next.Number)

	require.NoError(t, lifecycle.Complete(t.Context(), next.ID, Completion{Outcome: CompletionProcessInterrupted}))
	baselineID, err := store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.DeploymentID, baselineID)
	original, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, original.Status)
	require.Len(t, original.Attempts, 1)
	assert.Equal(t, deploymentmodel.Succeeded, original.Attempts[0].Status)
	current, err := store.Get(t.Context(), next.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, 1, consecutiveFailures(current))
}

func TestHistoricalFailureAfterSuccessKeepsBaselineAndBackoff(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	desired := desiredServices("api", "worker")
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desired, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))

	// Simulate a row written when a deployment allowed a later Attempt after success.
	later := newAttempt(first.DeploymentID, 2, first.StartedAt.Add(time.Second))
	later.TargetServices = []string{"worker"}
	later.Status, later.FinishedAt = deploymentmodel.Interrupted, &later.StartedAt
	require.NoError(t, store.InsertLegacyAttempt(t.Context(), later))
	_, err = db.Exec(t.Context(), "UPDATE deployments SET status = ? WHERE id = ?", later.Status, first.DeploymentID)
	require.NoError(t, err)

	_, err = store.Baseline(t.Context(), "app")
	require.NoError(t, err)
	current, err := store.Get(t.Context(), first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, 1, consecutiveFailures(current))
	history, err := store.ListServiceDeployments(t.Context(), "app", "api", 5)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, deploymentmodel.Succeeded, history[0].Status)
}
