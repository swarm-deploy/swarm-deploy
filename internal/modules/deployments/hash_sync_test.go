package deployments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
)

func TestHashAwareSyncRetriesSkippedServiceWithSameDesiredDigest(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	d1 := desiredServices("api")
	d1.Compose.Services[0].Image = "api:v1"
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "commit-1", d1, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	stateD1, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)

	d2 := desiredServices("api")
	d2.Compose.Services[0].Image = "api:v2"
	partial, err := lifecycle.StartIfChanged(t.Context(), "app", "commit-2", d2, true)
	require.NoError(t, err)
	require.NotNil(t, partial)
	partialDeployment, err := store.Get(t.Context(), partial.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, partialDeployment.PendingServices)
	require.NoError(t, lifecycle.Complete(t.Context(), partial.ID, Completion{
		Outcome: CompletionSucceeded, AppliedServices: []string{}, SkippedServices: []string{"api"},
	}))

	partialState, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.NotEqual(t, stateD1.Services["api"].DesiredDigest, partialState.Services["api"].DesiredDigest)
	assert.Equal(t, stateD1.Services["api"].AppliedDigest, partialState.Services["api"].AppliedDigest)
	assert.Equal(t, stateD1.AppliedDigest, partialState.AppliedDigest)
	partialDeployment, err = store.Get(t.Context(), partial.DeploymentID)
	require.NoError(t, err)
	require.Len(t, partialDeployment.Attempts, 1)
	assert.Equal(t, []string{"api"}, partialDeployment.Attempts[0].SkippedServices)
	assert.Empty(t, partialDeployment.Attempts[0].ConfirmedServiceDigests)
	_, err = store.BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err, "the D1 baseline remains valid")

	retry, err := lifecycle.StartIfChanged(t.Context(), "app", "commit-2", d2, false)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.NotEqual(t, partial.DeploymentID, retry.DeploymentID, "successful partial history must remain immutable")
	assert.Equal(t, []string{"api"}, mustDeployment(t, store, retry.DeploymentID).PendingServices)
	require.NoError(t, lifecycle.Complete(t.Context(), retry.ID, Completion{Outcome: CompletionSucceeded}))

	converged, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, converged.DesiredDigest, converged.AppliedDigest)
	assert.Equal(t, converged.Services["api"].DesiredDigest, converged.Services["api"].AppliedDigest)
	retryDeployment := mustDeployment(t, store, retry.DeploymentID)
	require.Len(t, retryDeployment.Attempts, 1)
	assert.Equal(t, converged.Services["api"].DesiredDigest,
		retryDeployment.Attempts[0].ConfirmedServiceDigests["api"])
	noOp, err := lifecycle.StartIfChanged(t.Context(), "app", "unrelated-commit", d2, true)
	require.NoError(t, err)
	assert.Nil(t, noOp, "a commit without an effective service change must remain a no-op")
}

func TestFailedAndInterruptedAttemptsDoNotAdvanceAppliedHashes(t *testing.T) {
	for _, outcome := range []CompletionOutcome{CompletionApplyFailed, CompletionProcessInterrupted} {
		t.Run(string(outcome), func(t *testing.T) {
			db, lifecycle, _ := openService(t)
			store := deploymentstore.NewStore(db)
			attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true)
			require.NoError(t, err)
			require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: outcome}))
			state, err := store.HashState(t.Context(), "app")
			require.NoError(t, err)
			assert.Empty(t, state.AppliedDigest)
			assert.Empty(t, state.Services["api"].AppliedDigest)
			assert.True(t, state.Services["api"].AppliedUncertain)
			assert.Equal(t, []string{"api"}, state.Pending().Services)
		})
	}
}

func TestFailedChangedServicePreservesPriorAppliedEvidence(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	d1 := desiredServices("api")
	d1.Compose.Services[0].Image = "api:v1"
	first, err := lifecycle.StartIfChanged(t.Context(), "app", "one", d1, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}))
	stateD1, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)

	d2 := desiredServices("api")
	d2.Compose.Services[0].Image = "api:v2"
	failed, err := lifecycle.StartIfChanged(t.Context(), "app", "two", d2, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), failed.ID, Completion{Outcome: CompletionApplyFailed}))
	state, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.NotEmpty(t, state.Services["api"].DesiredDigest)
	assert.Equal(t, stateD1.Services["api"].AppliedDigest, state.Services["api"].AppliedDigest)
	assert.Equal(t, stateD1.AppliedDigest, state.AppliedDigest)
	assert.True(t, state.Services["api"].AppliedUncertain)
	assert.NotEqual(t, state.Services["api"].DesiredDigest, state.Services["api"].AppliedDigest,
		"a failed attempt must retain the last successfully confirmed service hash")
	assert.Equal(t, []string{"api"}, state.Pending().Services)
}

func TestNewAndRemovedServicesRemainPendingUntilConfirmed(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	initial, err := lifecycle.StartIfChanged(t.Context(), "app", "one", desiredServices("api"), true)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, mustDeployment(t, store, initial.DeploymentID).PendingServices)
	require.NoError(t, lifecycle.Complete(t.Context(), initial.ID, Completion{Outcome: CompletionSucceeded}))

	removed, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desiredServices(), true)
	require.NoError(t, err)
	removedDeployment := mustDeployment(t, store, removed.DeploymentID)
	assert.Equal(t, []string{"api"}, removedDeployment.PendingRemovals)
	require.NoError(t, lifecycle.Complete(t.Context(), removed.ID, Completion{
		Outcome: CompletionSucceeded, ConfirmedRemovals: []string{}, SkippedRemovals: []string{"api"},
	}))
	state, err := store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, state.Pending().Removals)

	retry, err := lifecycle.StartIfChanged(t.Context(), "app", "two", desiredServices(), false)
	require.NoError(t, err)
	assert.NotEqual(t, removed.DeploymentID, retry.DeploymentID)
	require.NoError(t, lifecycle.Complete(t.Context(), retry.ID, Completion{Outcome: CompletionSucceeded}))
	state, err = store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.Empty(t, state.Pending().Removals)
	assert.Empty(t, state.Services)
}

func TestAppliedHashPersistenceFailureLeavesAttemptRetryable(t *testing.T) {
	db, lifecycle, _ := openService(t)
	store := deploymentstore.NewStore(db)
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), `CREATE TRIGGER reject_applied_hash
		BEFORE UPDATE OF applied_digest ON deployment_service_states
		WHEN NEW.applied_digest IS NOT NULL
		BEGIN SELECT RAISE(FAIL, 'synthetic applied hash failure'); END`)
	require.NoError(t, err)

	err = lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded})
	require.ErrorContains(t, err, "synthetic applied hash failure")
	state, stateErr := store.HashState(t.Context(), "app")
	require.NoError(t, stateErr)
	assert.Empty(t, state.AppliedDigest)
	assert.Empty(t, state.Services["api"].AppliedDigest)
	stored := mustDeployment(t, store, attempt.DeploymentID)
	assert.Equal(t, deploymentmodel.Running, stored.Status, "completion and hash confirmation roll back atomically")

	_, err = db.Exec(t.Context(), "DROP TRIGGER reject_applied_hash")
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded}))
	state, err = store.HashState(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, state.DesiredDigest, state.AppliedDigest)
	assert.Equal(t, state.Services["api"].DesiredDigest, state.Services["api"].AppliedDigest)
}

func mustDeployment(t *testing.T, store *deploymentstore.Store, id string) deploymentmodel.Deployment {
	t.Helper()
	deployment, err := store.Get(t.Context(), id)
	require.NoError(t, err)
	return deployment
}
