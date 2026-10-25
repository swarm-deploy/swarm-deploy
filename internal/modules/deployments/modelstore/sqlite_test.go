package modelstore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestDeploymentPersistenceAndSuccessfulBaseline(t *testing.T) {
	ctx := t.Context()
	store := NewStore(testutil.OpenSQLite(t))
	desired, err := deploymentmodel.Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:1",
	}}}})
	require.NoError(t, err)
	startedAt := time.Date(2026, time.October, 25, 10, 0, 0, 0, time.UTC)
	attempt := deploymentmodel.Attempt{
		ID: "attempt-1", DeploymentID: "deployment-1", Number: 1, Status: deploymentmodel.Running,
		StartedAt: startedAt, Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending, CleanupStatus: deploymentmodel.StagePending,
		ActualStateStatus: deploymentmodel.ActualStateUnknown,
	}
	deployment := deploymentmodel.Deployment{
		ID: "deployment-1", Stack: "app", Commit: "commit-1", Status: deploymentmodel.Running,
		StartedAt: startedAt, Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending, CleanupStatus: deploymentmodel.StagePending,
		ActualStateStatus: deploymentmodel.ActualStateUnknown,
		ComparisonBasis: deploymentmodel.BasisNone, ComparisonStatus: deploymentmodel.ComparisonUnknown,
		DiffAvailable: true, LatestAttemptID: attempt.ID, AttemptCount: 1,
	}
	require.NoError(t, store.CreateDeployment(ctx, deployment, desired, attempt))

	stored, err := store.Get(ctx, deployment.ID)
	require.NoError(t, err)
	require.Len(t, stored.Attempts, 1)
	assert.Equal(t, attempt, stored.Attempts[0])
	assert.Equal(t, deploymentmodel.Running, stored.Status)

	finishedAt := startedAt.Add(time.Minute)
	attempt.Status, attempt.FinishedAt = deploymentmodel.Succeeded, &finishedAt
	attempt.Phase, attempt.ApplyStatus = deploymentmodel.PhaseCompleted, deploymentmodel.StageSucceeded
	attempt.VerificationStatus, attempt.CleanupStatus = deploymentmodel.StageSkipped, deploymentmodel.StageSucceeded
	require.NoError(t, store.FinishAttempt(ctx, deployment, attempt))
	require.NoError(t, store.SaveBaseline(ctx, deployment.ID))

	stored, err = store.Get(ctx, deployment.ID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, stored.Status)
	baseline, err := store.Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, desired.Digest, baseline.Digest)
}
