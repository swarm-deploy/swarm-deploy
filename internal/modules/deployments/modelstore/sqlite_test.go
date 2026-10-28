package modelstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
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
		ComparisonBasis:   deploymentmodel.BasisNone, ComparisonStatus: deploymentmodel.ComparisonUnknown,
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

func TestInitJobExecutionPersistenceOrderingLogsAndCascade(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenSQLite(t)
	store := NewStore(db)
	desired, err := deploymentmodel.Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:1",
	}}}})
	require.NoError(t, err)
	startedAt := time.Date(2026, time.October, 25, 10, 0, 0, 0, time.UTC)
	attempt1 := deploymentmodel.Attempt{
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
		ComparisonBasis:   deploymentmodel.BasisNone, ComparisonStatus: deploymentmodel.ComparisonUnknown,
		DiffAvailable: true, LatestAttemptID: attempt1.ID, AttemptCount: 1,
	}
	require.NoError(t, store.CreateDeployment(ctx, deployment, desired, attempt1))

	emptyLogs := []byte{}
	firstFinished := startedAt.Add(time.Second)
	require.NoError(t, store.SaveInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt1.ID, Ordinal: 1, ServiceName: "api", JobName: "migrate",
		Status: deploymentmodel.InitJobSucceeded, StartedAt: startedAt, FinishedAt: firstFinished,
		Logs: emptyLogs, LogsAvailable: true,
	}))
	require.NoError(t, store.SaveInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt1.ID, Ordinal: 2, ServiceName: "api", JobName: "seed",
		Status: deploymentmodel.InitJobFailed, StartedAt: firstFinished, FinishedAt: firstFinished.Add(time.Second),
		LogsAvailable: false,
	}))

	attempt1.Status, attempt1.FinishedAt = deploymentmodel.Failed, ptrTime(firstFinished.Add(2*time.Second))
	attempt1.Phase, attempt1.ApplyStatus = deploymentmodel.PhaseApply, deploymentmodel.StageFailed
	attempt1.VerificationStatus, attempt1.CleanupStatus = deploymentmodel.StageSkipped, deploymentmodel.StageSkipped
	require.NoError(t, store.FinishAttempt(ctx, deployment, attempt1))
	attempt2 := deploymentmodel.Attempt{
		ID: "attempt-2", DeploymentID: deployment.ID, Number: 2, Status: deploymentmodel.Running,
		StartedAt: firstFinished.Add(3 * time.Second), Phase: deploymentmodel.PhaseApply,
		ApplyStatus: deploymentmodel.StageRunning, VerificationStatus: deploymentmodel.StagePending,
		CleanupStatus: deploymentmodel.StagePending, ActualStateStatus: deploymentmodel.ActualStateUnknown,
	}
	require.NoError(t, store.StartAttempt(ctx, deployment, attempt2))
	boundedLogs := bytes.Repeat([]byte("x"), 64*1024)
	require.NoError(t, store.SaveInitJobExecution(ctx, deploymentmodel.InitJobExecution{
		AttemptID: attempt2.ID, Ordinal: 1, ServiceName: "api", JobName: "migrate",
		Status: deploymentmodel.InitJobInterrupted, StartedAt: attempt2.StartedAt,
		FinishedAt: attempt2.StartedAt.Add(time.Second), Logs: boundedLogs,
		LogsAvailable: true, LogsTruncated: true,
	}))

	stored, err := store.Get(ctx, deployment.ID)
	require.NoError(t, err)
	require.Len(t, stored.Attempts, 2)
	assert.Equal(t, attempt2.ID, stored.Attempts[0].ID)
	assert.Equal(t, attempt1.ID, stored.Attempts[1].ID)
	require.Len(t, stored.Attempts[0].InitJobs, 1)
	require.Len(t, stored.Attempts[1].InitJobs, 2)
	assert.Equal(t, []int{1, 2}, []int{
		stored.Attempts[1].InitJobs[0].Ordinal,
		stored.Attempts[1].InitJobs[1].Ordinal,
	})
	assert.True(t, stored.Attempts[0].InitJobs[0].LogsTruncated)
	assert.True(t, stored.Attempts[1].InitJobs[0].LogsAvailable)
	assert.False(t, stored.Attempts[1].InitJobs[1].LogsAvailable)

	logs, err := store.InitJobLogs(ctx, deployment.ID, attempt1.ID, 1)
	require.NoError(t, err)
	assert.NotNil(t, logs, "successfully collected empty output must not become unavailable")
	assert.Empty(t, logs)
	_, err = store.InitJobLogs(ctx, deployment.ID, attempt1.ID, 2)
	require.ErrorIs(t, err, ErrLogsUnavailable)
	logs, err = store.InitJobLogs(ctx, deployment.ID, attempt2.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, boundedLogs, logs)
	_, err = store.InitJobLogs(ctx, "other-deployment", attempt2.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = db.Exec(ctx, "DELETE FROM deployment_attempts WHERE id=?", attempt2.ID)
	require.NoError(t, err)
	var count int
	require.NoError(t, db.Get(ctx, &count, "SELECT count(*) FROM deployment_init_jobs WHERE attempt_id=?", attempt2.ID))
	assert.Zero(t, count, "attempt deletion must cascade to Init Job executions")
	_, err = db.Exec(ctx, "DELETE FROM deployments WHERE id=?", deployment.ID)
	require.NoError(t, err)
	require.NoError(t, db.Get(ctx, &count, "SELECT count(*) FROM deployment_init_jobs"))
	assert.Zero(t, count, "deployment deletion must cascade through Attempts to Init Job executions")
}

func TestSaveInitJobExecutionRejectsMissingParentAndTimestamps(t *testing.T) {
	store := NewStore(testutil.OpenSQLite(t))
	for _, testCase := range []struct {
		name      string
		execution deploymentmodel.InitJobExecution
		want      error
	}{
		{name: "timestamps", execution: deploymentmodel.InitJobExecution{AttemptID: "missing"},
			want: errors.New("init job execution timestamps are required")},
		{name: "parent", execution: deploymentmodel.InitJobExecution{
			AttemptID: "missing", Ordinal: 1, Status: deploymentmodel.InitJobSucceeded,
			StartedAt: time.Now(), FinishedAt: time.Now(),
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := store.SaveInitJobExecution(t.Context(), testCase.execution)
			require.Error(t, err)
			if testCase.want != nil {
				assert.EqualError(t, err, testCase.want.Error())
			}
		})
	}
}

func TestSupersededProjectionPreservesStoredOutcome(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenSQLite(t)
	store := NewStore(db)
	desired, err := deploymentmodel.Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:1",
	}}}})
	require.NoError(t, err)
	startedAt := time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC)
	first, firstAttempt := runningDeploymentFixture("deployment-1", "attempt-1", "app", startedAt)
	require.NoError(t, store.CreateDeployment(ctx, first, desired, firstAttempt))
	finishedAt := startedAt.Add(time.Minute)
	firstAttempt.Status, firstAttempt.FinishedAt = deploymentmodel.Failed, &finishedAt
	firstAttempt.ErrorCode, firstAttempt.ErrorDiagnostic = "apply_failed", "preserved diagnostic"
	firstAttempt.Phase, firstAttempt.ApplyStatus = deploymentmodel.PhaseApply, deploymentmodel.StageFailed
	firstAttempt.VerificationStatus, firstAttempt.CleanupStatus = deploymentmodel.StageSkipped, deploymentmodel.StageSkipped
	require.NoError(t, store.FinishAttempt(ctx, first, firstAttempt))

	second, secondAttempt := runningDeploymentFixture("deployment-2", "attempt-2", "app", startedAt.Add(2*time.Minute))
	second.Commit = "commit-2"
	require.NoError(t, store.CreateDeployment(ctx, second, desired, secondAttempt))

	projected, err := store.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Superseded, projected.Status)
	assert.Equal(t, "preserved diagnostic", projected.ErrorDiagnostic)
	var raw struct {
		Status     string `db:"status"`
		Diagnostic string `db:"diagnostic"`
	}
	require.NoError(t, db.Get(ctx, &raw, `SELECT status,
		json_extract(details_payload,'$.error_diagnostic') AS diagnostic
		FROM deployments WHERE id=?`, first.ID))
	assert.Equal(t, "failed", raw.Status)
	assert.Equal(t, "preserved diagnostic", raw.Diagnostic)
}

func TestListDoesNotDecodeFullChangePayloads(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenSQLite(t)
	store := NewStore(db)
	desired, err := deploymentmodel.Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:1",
	}}}})
	require.NoError(t, err)
	deployment, attempt := runningDeploymentFixture(
		"deployment-1", "attempt-1", "app", time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, store.CreateDeployment(ctx, deployment, desired, attempt))
	_, err = db.Exec(ctx, "UPDATE deployments SET changes_payload=json_array('not-an-object') WHERE id=?", deployment.ID)
	require.NoError(t, err)

	_, err = store.List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err, "list must not decode full change payloads")
	_, err = store.Get(ctx, deployment.ID)
	require.Error(t, err, "detail still validates and decodes its change payload")
}

func TestDeploymentPayloadDoesNotDuplicateColumns(t *testing.T) {
	ctx := t.Context()
	db := testutil.OpenSQLite(t)
	store := NewStore(db)
	desired, err := deploymentmodel.Prepare(compose.File{Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: "api:1",
	}}}})
	require.NoError(t, err)
	deployment, attempt := runningDeploymentFixture(
		"deployment-1", "attempt-1", "app", time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, store.CreateDeployment(ctx, deployment, desired, attempt))

	var row struct {
		ErrorCode string                `db:"error_code"`
		Phase     deploymentmodel.Phase `db:"phase"`
		Details   []byte                `db:"details_payload"`
	}
	require.NoError(t, db.Get(ctx, &row,
		"SELECT error_code,phase,details_payload FROM deployments WHERE id=?", deployment.ID))
	assert.Empty(t, row.ErrorCode)
	assert.Equal(t, deploymentmodel.PhaseApply, row.Phase)
	var details map[string]any
	require.NoError(t, json.Unmarshal(row.Details, &details))
	for _, key := range []string{"id", "stack", "commit", "status", "error_code", "phase", "started_at", "finished_at"} {
		assert.NotContains(t, details, key)
	}
}

func TestDeploymentIndexesSupportListQueries(t *testing.T) {
	db := testutil.OpenSQLite(t)
	query := `SELECT id FROM deployments WHERE status='failed'
		ORDER BY started_at DESC,id LIMIT 21`
	var rows []struct {
		ID     int    `db:"id"`
		Parent int    `db:"parent"`
		Unused int    `db:"notused"`
		Detail string `db:"detail"`
	}
	require.NoError(t, db.Select(t.Context(), &rows, "EXPLAIN QUERY PLAN "+query))
	var plan strings.Builder
	for _, row := range rows {
		plan.WriteString(row.Detail)
	}
	t.Log(plan.String())
	assert.Contains(t, plan.String(), "idx_deployments_status_time")
	assert.NotContains(t, plan.String(), "USE TEMP B-TREE FOR ORDER BY")
}

func runningDeploymentFixture(
	deploymentID string,
	attemptID string,
	stack string,
	startedAt time.Time,
) (deploymentmodel.Deployment, deploymentmodel.Attempt) {
	attempt := deploymentmodel.Attempt{
		ID: attemptID, DeploymentID: deploymentID, Number: 1, Status: deploymentmodel.Running,
		StartedAt: startedAt, Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending, CleanupStatus: deploymentmodel.StagePending,
		ActualStateStatus: deploymentmodel.ActualStateUnknown,
	}
	deployment := deploymentmodel.Deployment{
		ID: deploymentID, Stack: stack, Commit: "commit-1", Status: deploymentmodel.Running,
		StartedAt: startedAt, Phase: deploymentmodel.PhaseApply, ApplyStatus: deploymentmodel.StageRunning,
		VerificationStatus: deploymentmodel.StagePending, CleanupStatus: deploymentmodel.StagePending,
		ActualStateStatus: deploymentmodel.ActualStateUnknown, ComparisonBasis: deploymentmodel.BasisNone,
		ComparisonStatus: deploymentmodel.ComparisonUnknown, DiffAvailable: true,
		LatestAttemptID: attemptID, AttemptCount: 1,
	}
	return deployment, attempt
}

func ptrTime(value time.Time) *time.Time { return &value }
