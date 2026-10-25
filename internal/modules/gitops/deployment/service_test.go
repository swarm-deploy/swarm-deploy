package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func desired(image string) compose.File {
	return compose.File{Path: "app.yaml", Digest: "source", Compose: compose.Compose{Services: compose.Services{{
		Name: "api", Image: image, Environment: compose.Environment{Map: map[string]string{"API_TOKEN": "plaintext-secret", "MODE": "production"}},
	}}}}
}

func legacyDeploymentDetailsPayload(t *testing.T, deployment deploymentmodel.Deployment) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		ErrorDiagnostic    string                            `json:"error_diagnostic,omitempty"`
		ApplyStatus        deploymentmodel.StageStatus       `json:"apply_status"`
		VerificationStatus deploymentmodel.StageStatus       `json:"verification_status"`
		CleanupStatus      deploymentmodel.StageStatus       `json:"cleanup_status"`
		ActualStateStatus  deploymentmodel.ActualStateStatus `json:"actual_state_status"`
		ObservedAt         *time.Time                        `json:"observed_at,omitempty"`
		ComparisonBasis    deploymentmodel.ComparisonBasis   `json:"comparison_basis"`
		ComparisonStatus   deploymentmodel.ComparisonStatus  `json:"comparison_status"`
		BasisDeploymentID  string                            `json:"basis_deployment_id,omitempty"`
		Summary            deploymentmodel.ChangeSummary     `json:"summary"`
		Resources          deploymentmodel.ResourceSummary   `json:"resources"`
		DiffAvailable      bool                              `json:"diff_available"`
	}{
		ErrorDiagnostic: deployment.ErrorDiagnostic, ApplyStatus: deployment.ApplyStatus,
		VerificationStatus: deployment.VerificationStatus, CleanupStatus: deployment.CleanupStatus,
		ActualStateStatus: deployment.ActualStateStatus, ObservedAt: deployment.ObservedAt,
		ComparisonBasis: deployment.ComparisonBasis, ComparisonStatus: deployment.ComparisonStatus,
		BasisDeploymentID: deployment.BasisDeploymentID, Summary: deployment.Summary,
		Resources: deployment.Resources, DiffAvailable: deployment.DiffAvailable,
	})
	require.NoError(t, err)
	return string(payload)
}

func TestSuccessfulDeploymentIsIndependentOfHistoryFailure(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	projection, err := history.NewSQLStore(db, 50)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", projection))
	service := NewService(db, bus)
	repo := deploymentstore.NewStore(db)
	attempt, err := service.StartIfChanged(ctx, "app", "commit", desired("api:1"), true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	current, err := repo.Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Running, current.Status)
	require.NoError(t, service.Succeed(ctx, attempt.ID, model.Stack{SourceDigest: "source", LastCommit: "commit"}))
	entries, err := projection.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, entries, "publish must not invoke history")
	_, err = db.Exec(ctx, "CREATE TRIGGER reject_history BEFORE INSERT ON event_history BEGIN SELECT RAISE(ABORT, 'projection failed'); END")
	require.NoError(t, err)
	_, err = bus.ProcessNext(ctx)
	require.NoError(t, err)
	current, err = repo.Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, current.Status)
	_, err = db.Exec(ctx, "DROP TRIGGER reject_history")
	require.NoError(t, err)
	_, err = db.Exec(ctx, "UPDATE outbox_deliveries SET available_at=0")
	require.NoError(t, err)
	_, err = bus.ProcessNext(ctx)
	require.NoError(t, err)
	entries, err = projection.Read(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	var pending int
	require.NoError(t, db.Get(ctx, &pending, "SELECT count(*) FROM outbox_events"))
	assert.Zero(t, pending)
	require.ErrorIs(t, service.Fail(ctx, attempt.ID, model.Stack{}, "apply_failed"), deploymentstore.ErrNotRunning)
	same, err := service.StartIfChanged(ctx, "app", "next-commit", desired("api:1"), true)
	require.NoError(t, err)
	assert.Nil(t, same)
}

func TestFailedCommitAndCrashKeepSuccessfulBaseline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	h, err := history.NewSQLStore(db, 50)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", h))
	service := NewService(db, bus)
	first, err := service.StartIfChanged(ctx, "app", "first", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, first.ID, model.Stack{LastCommit: "first", SourceDigest: "first"}))
	second, err := service.StartIfChanged(ctx, "app", "second", desired("api:2"), true)
	require.NoError(t, err)
	_, err = db.Exec(ctx, "CREATE TRIGGER reject_publication BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT, 'publish failed'); END")
	require.NoError(t, err)
	require.Error(t, service.Succeed(ctx, second.ID, model.Stack{LastCommit: "second", SourceDigest: "second"}))
	baseline, err := deploymentstore.NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:1", baseline.Definition.Compose.Services[0].Image)
	state, err := modelstore.NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "first", state.Stacks["app"].LastCommit)
	require.NoError(t, db.Close())
	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ctx, "DROP TRIGGER reject_publication")
	require.NoError(t, err)
	bus = eventbus.NewOutboxDispatcher(outbox.New(db))
	h, err = history.NewSQLStore(db, 50)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployInterrupted, "interrupted-history", h))
	service = NewService(db, bus)
	require.NoError(t, service.InterruptRunning(ctx))
	require.NoError(t, service.InterruptRunning(ctx))
	current, err := deploymentstore.NewStore(db).Get(ctx, second.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Interrupted, current.Status)
	var interruptedEvents int
	require.NoError(t, db.Get(ctx, &interruptedEvents,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameDeployInterrupted))
	assert.Equal(t, 1, interruptedEvents, "orphan recovery must publish exactly one interruption")
	baseline, err = deploymentstore.NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:1", baseline.Definition.Compose.Services[0].Image)
	retry, err := service.StartIfChanged(ctx, "app", "second", desired("api:2"), true)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, second.DeploymentID, retry.DeploymentID)
	assert.NotEqual(t, second.ID, retry.ID)
	require.NoError(t, service.Succeed(ctx, retry.ID, model.Stack{LastCommit: "second", SourceDigest: "second"}))
	baseline, err = deploymentstore.NewStore(db).Baseline(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, "api:2", baseline.Definition.Compose.Services[0].Image)
}

func TestRetryReusesDeploymentAndPreservesAttemptHistoryAndDesiredChanges(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	store := deploymentstore.NewStore(db)

	baselineAttempt, err := service.StartIfChanged(ctx, "app", "baseline", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, baselineAttempt.ID, model.Stack{}))
	first, err := service.StartIfChanged(ctx, "app", "desired", desired("api:2"), true)
	require.NoError(t, err)
	require.NoError(t, service.FailApplyWithError(ctx, first.ID, model.Stack{}, "apply_failed",
		fmt.Errorf("first diagnostic")))
	beforeRetry, err := store.Get(ctx, first.DeploymentID)
	require.NoError(t, err)
	require.Len(t, beforeRetry.Changes, 1)

	retry, err := service.StartIfChanged(ctx, "app", "new-commit-same-state", desired("api:2"), true)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, first.DeploymentID, retry.DeploymentID)
	assert.NotEqual(t, first.ID, retry.ID)
	require.NoError(t, service.FailApplyWithError(ctx, retry.ID, model.Stack{}, "apply_failed",
		fmt.Errorf("second diagnostic")))
	afterRetry, err := store.Get(ctx, first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, beforeRetry.Changes, afterRetry.Changes)
	assert.Equal(t, beforeRetry.BasisDeploymentID, afterRetry.BasisDeploymentID)
	assert.Equal(t, 2, afterRetry.AttemptCount)
	require.Len(t, afterRetry.Attempts, 2)
	assert.Equal(t, "second diagnostic", afterRetry.Attempts[0].ErrorDiagnostic)
	assert.Equal(t, "first diagnostic", afterRetry.Attempts[1].ErrorDiagnostic)

	third, err := service.StartIfChanged(ctx, "app", "same-again", desired("api:2"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, third.ID, model.Stack{}))
	noExecution, err := service.StartIfChanged(ctx, "app", "ordinary-reconcile", desired("api:2"), true)
	require.NoError(t, err)
	assert.Nil(t, noExecution)
	succeeded, err := store.Get(ctx, first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, 3, succeeded.AttemptCount)
}

func TestRetryAdoptsLegacyDeploymentProjection(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	store := deploymentstore.NewStore(db)

	legacyDesired, err := deploymentmodel.Prepare(desired("api:2"))
	require.NoError(t, err)
	desiredPayload, err := json.Marshal(legacyDesired)
	require.NoError(t, err)
	detailsPayload := legacyDeploymentDetailsPayload(t, deploymentmodel.Deployment{
		ErrorDiagnostic: "legacy diagnostic", ApplyStatus: deploymentmodel.StageFailed,
		VerificationStatus: deploymentmodel.StageSkipped, CleanupStatus: deploymentmodel.StageSkipped,
		ActualStateStatus: deploymentmodel.ActualStateUnknown, ComparisonBasis: deploymentmodel.BasisNone,
		ComparisonStatus: deploymentmodel.ComparisonUnknown, DiffAvailable: true,
	})
	started := time.Now().UTC().Add(-time.Minute)
	finished := started.Add(time.Second)
	const deploymentID = "legacy-deployment"
	_, err = db.Exec(ctx, `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		deploymentID, "app", "desired", "failed", "apply_failed", "apply", started, finished,
		string(desiredPayload), string(detailsPayload), `[]`)
	require.NoError(t, err)

	legacy, err := store.Get(ctx, deploymentID)
	require.NoError(t, err)
	assert.Empty(t, legacy.LatestAttemptID)
	assert.Zero(t, legacy.AttemptCount)
	assert.Empty(t, legacy.Attempts)
	retry, err := service.StartIfChanged(ctx, "app", "same-state", desired("api:2"), true)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, 1, retry.Number)
	require.NoError(t, service.Fail(ctx, retry.ID, model.Stack{}, "apply_failed"))

	stored, err := store.Get(ctx, deploymentID)
	require.NoError(t, err)
	assert.Equal(t, retry.ID, stored.LatestAttemptID)
	assert.Equal(t, 1, stored.AttemptCount)
	require.Len(t, stored.Attempts, 1)
}

func TestLegacyRunningDeploymentIsRecoveredBeforeRetry(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	store := deploymentstore.NewStore(db)

	legacyDesired, err := deploymentmodel.Prepare(desired("api:2"))
	require.NoError(t, err)
	desiredPayload, err := json.Marshal(legacyDesired)
	require.NoError(t, err)
	detailsPayload := legacyDeploymentDetailsPayload(t, deploymentmodel.Deployment{
		ApplyStatus: deploymentmodel.StageRunning, VerificationStatus: deploymentmodel.StagePending,
		CleanupStatus: deploymentmodel.StagePending, ActualStateStatus: deploymentmodel.ActualStateUnknown,
		ComparisonBasis: deploymentmodel.BasisNone, ComparisonStatus: deploymentmodel.ComparisonUnknown, DiffAvailable: true,
	})
	started := time.Now().UTC().Add(-time.Minute)
	const deploymentID = "legacy-running-deployment"
	_, err = db.Exec(ctx, `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,NULL,?,?,?)`,
		deploymentID, "app", "desired", "running", "", "apply", started,
		string(desiredPayload), string(detailsPayload), `[]`)
	require.NoError(t, err)

	require.NoError(t, service.InterruptRunning(ctx))
	require.NoError(t, service.InterruptRunning(ctx))
	recovered, err := store.Get(ctx, deploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Interrupted, recovered.Status)
	assert.Equal(t, 1, recovered.AttemptCount)
	require.Len(t, recovered.Attempts, 1)
	assert.Equal(t, deploymentmodel.Interrupted, recovered.Attempts[0].Status)
	assert.Equal(t, "process_interrupted", recovered.Attempts[0].ErrorCode)

	retry, err := service.StartIfChanged(ctx, "app", "same-state", desired("api:2"), true)
	require.NoError(t, err)
	require.NotNil(t, retry)
	assert.Equal(t, 2, retry.Number)
}

func TestSupersessionPreservesStoredStatusAndDiagnostics(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	store := deploymentstore.NewStore(db)

	first, err := service.StartIfChanged(ctx, "app", "first", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.FailApplyWithError(ctx, first.ID, model.Stack{}, "apply_failed",
		fmt.Errorf("preserved diagnostic")))
	_, err = service.StartIfChanged(ctx, "app", "second", desired("api:2"), true)
	require.NoError(t, err)

	projected, err := store.Get(ctx, first.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Superseded, projected.Status)
	assert.Equal(t, "preserved diagnostic", projected.ErrorDiagnostic)
	var raw struct {
		Status     string `db:"status"`
		Diagnostic string `db:"diagnostic"`
	}
	require.NoError(t, db.Get(ctx, &raw, `SELECT status,
		json_extract(details_payload,'$.error_diagnostic') AS diagnostic
		FROM deployments WHERE id=?`, first.DeploymentID))
	assert.Equal(t, "failed", raw.Status)
	assert.Equal(t, "preserved diagnostic", raw.Diagnostic)

	superseded, err := store.List(ctx, deploymentmodel.ListFilter{Status: deploymentmodel.Superseded})
	require.NoError(t, err)
	require.Len(t, superseded.Deployments, 1)
	assert.Equal(t, first.DeploymentID, superseded.Deployments[0].ID)
	failed, err := store.List(ctx, deploymentmodel.ListFilter{Status: deploymentmodel.Failed})
	require.NoError(t, err)
	assert.Empty(t, failed.Deployments)
}

func TestRollbackToBaselineRetriesAfterUnsuccessfulAttempt(t *testing.T) {
	for _, status := range []deploymentmodel.Status{deploymentmodel.Failed, deploymentmodel.Interrupted} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			db, err := storage.Open(ctx, t.TempDir())
			require.NoError(t, err)
			defer db.Close()
			service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
			first, err := service.StartIfChanged(ctx, "app", "a", desired("api:a"), true)
			require.NoError(t, err)
			require.NoError(t, service.Succeed(ctx, first.ID, model.Stack{LastCommit: "a"}))
			second, err := service.StartIfChanged(ctx, "app", "b", desired("api:b"), true)
			require.NoError(t, err)
			require.NotNil(t, second)
			if status == deploymentmodel.Failed {
				require.NoError(t, service.Fail(ctx, second.ID, model.Stack{}, "prune_failed"))
			} else {
				require.NoError(t, service.InterruptStack(ctx, "app"))
			}
			rollback, err := service.StartIfChanged(ctx, "app", "rollback", desired("api:a"), true)
			require.NoError(t, err)
			require.NotNil(t, rollback)
			assert.NotEqual(t, second.DeploymentID, rollback.DeploymentID)
			stored, getErr := deploymentstore.NewStore(db).Get(ctx, rollback.DeploymentID)
			require.NoError(t, getErr)
			assert.Equal(t, deploymentmodel.BasisSuccessfulBaseline, stored.ComparisonBasis)
			assert.Equal(t, deploymentmodel.ComparisonUnknown, stored.ComparisonStatus)
			assert.Equal(t, first.DeploymentID, stored.BasisDeploymentID)
			assert.Empty(t, stored.Changes, "changes must stay relative to the successful baseline")
		})
	}
}

func TestListUsesLightweightCursorSummaries(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	clock := time.Date(2026, time.October, 10, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	for i, image := range []string{"api:1", "api:2", "api:3"} {
		attempt, startErr := service.StartIfChanged(ctx, "app", string(rune('a'+i)), desired(image), true)
		require.NoError(t, startErr)
		require.NoError(t, service.Succeed(ctx, attempt.ID, model.Stack{}))
	}
	store := deploymentstore.NewStore(db)
	first, err := store.List(ctx, deploymentmodel.ListFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Deployments, 2)
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, "c", first.Deployments[0].Commit)
	assert.Equal(t, deploymentmodel.StageSucceeded, first.Deployments[0].ApplyStatus)
	assert.Equal(t, deploymentmodel.ActualStateUnknown, first.Deployments[0].ActualStateStatus)
	assert.Equal(t, deploymentmodel.StageSkipped, first.Deployments[0].VerificationStatus)
	assert.Equal(t, deploymentmodel.ComparisonUnknown, first.Deployments[0].ComparisonStatus)
	assert.NotZero(t, first.Deployments[0].Summary.Changed)
	second, err := store.List(ctx, deploymentmodel.ListFilter{Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Deployments, 1)
	assert.Empty(t, second.NextCursor)
	assert.Equal(t, "a", second.Deployments[0].Commit)

	_, err = db.Exec(ctx, "UPDATE deployments SET changes_payload=json_array('not-an-object')")
	require.NoError(t, err)
	_, err = store.List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err, "list must not decode full change payloads")
	_, err = store.Get(ctx, first.Deployments[0].ID)
	require.Error(t, err, "detail still validates and decodes its change payload")
	_, err = store.List(ctx, deploymentmodel.ListFilter{Cursor: "malformed"})
	require.ErrorIs(t, err, deploymentstore.ErrInvalidCursor)
}

func TestListFiltersStatusBeforeCursorPagination(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	clock := time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	for index, stack := range []string{"app-0", "other", "app-2", "app-3"} {
		attempt, startErr := service.StartIfChanged(ctx, stack, fmt.Sprintf("commit-%d", index),
			desired(fmt.Sprintf("api:%d", index)), true)
		require.NoError(t, startErr)
		if index == 1 {
			require.NoError(t, service.Succeed(ctx, attempt.ID, model.Stack{}))
			continue
		}
		require.NoError(t, service.Fail(ctx, attempt.ID, model.Stack{}, "apply_failed"))
	}

	store := deploymentstore.NewStore(db)
	first, err := store.List(ctx, deploymentmodel.ListFilter{Status: deploymentmodel.Failed, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Deployments, 2)
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, []string{"commit-3", "commit-2"}, []string{first.Deployments[0].Commit, first.Deployments[1].Commit})
	second, err := store.List(ctx, deploymentmodel.ListFilter{Status: deploymentmodel.Failed, Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Deployments, 1)
	assert.Equal(t, "commit-0", second.Deployments[0].Commit)
	assert.Empty(t, second.NextCursor)
}

func TestDeploymentColumnsStaySynchronizedAndSupportFailureFilters(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	repo := deploymentstore.NewStore(db)

	type completionCase struct {
		name     string
		code     string
		phase    deploymentmodel.Phase
		status   deploymentmodel.Status
		complete func(string) error
	}
	testCases := []completionCase{
		{name: "successful", phase: deploymentmodel.PhaseCompleted, status: deploymentmodel.Succeeded,
			complete: func(id string) error { return service.Succeed(ctx, id, model.Stack{}) }},
		{name: "apply error", code: "apply_failed", phase: deploymentmodel.PhaseApply, status: deploymentmodel.Failed,
			complete: func(id string) error { return service.FailApply(ctx, id, model.Stack{}, "apply_failed") }},
		{name: "verification error", code: "verification_unknown", phase: deploymentmodel.PhaseVerification, status: deploymentmodel.Interrupted,
			complete: func(id string) error { return service.InterruptVerification(ctx, id, model.Stack{}) }},
		{name: "cleanup error", code: "prune_failed", phase: deploymentmodel.PhaseCleanup, status: deploymentmodel.Failed,
			complete: func(id string) error {
				return service.FailCleanup(ctx, id, model.Stack{}, "prune_failed", time.Now().UTC())
			}},
	}
	ids := make(map[string]string, len(testCases))
	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			attempt, startErr := service.StartIfChanged(ctx, testCase.name, fmt.Sprintf("commit-%d", index),
				desired(fmt.Sprintf("api:%d", index)), true)
			require.NoError(t, startErr)
			require.NotNil(t, attempt)
			assertDeploymentColumns(t, db, attempt.DeploymentID, "", deploymentmodel.PhaseApply)
			require.NoError(t, testCase.complete(attempt.ID))
			assertDeploymentColumns(t, db, attempt.DeploymentID, testCase.code, testCase.phase)
			stored, getErr := repo.Get(ctx, attempt.DeploymentID)
			require.NoError(t, getErr)
			assert.Equal(t, testCase.status, stored.Status)
			ids[testCase.name] = attempt.DeploymentID
		})
	}

	filterCases := []struct {
		name   string
		filter deploymentmodel.ListFilter
		wantID string
	}{
		{name: "error code", filter: deploymentmodel.ListFilter{ErrorCode: "verification_unknown"}, wantID: ids["verification error"]},
		{name: "phase", filter: deploymentmodel.ListFilter{Phase: deploymentmodel.PhaseApply}, wantID: ids["apply error"]},
		{name: "combined", filter: deploymentmodel.ListFilter{ErrorCode: "prune_failed", Phase: deploymentmodel.PhaseCleanup},
			wantID: ids["cleanup error"]},
	}
	for _, filterCase := range filterCases {
		t.Run("filter "+filterCase.name, func(t *testing.T) {
			filtered, listErr := repo.List(ctx, filterCase.filter)
			require.NoError(t, listErr)
			require.Len(t, filtered.Deployments, 1)
			assert.Equal(t, filterCase.wantID, filtered.Deployments[0].ID)
		})
	}
}

func TestDeploymentIndexesSupportListQueries(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	testCases := []struct {
		name      string
		query     string
		wantIndex string
	}{
		{name: "status", query: `SELECT id FROM deployments WHERE status='failed'
			ORDER BY started_at DESC,id LIMIT 21`, wantIndex: "idx_deployments_status_time"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var rows []struct {
				ID     int    `db:"id"`
				Parent int    `db:"parent"`
				Unused int    `db:"notused"`
				Detail string `db:"detail"`
			}
			require.NoError(t, db.Select(t.Context(), &rows, "EXPLAIN QUERY PLAN "+testCase.query))
			var plan strings.Builder
			for _, row := range rows {
				plan.WriteString(row.Detail)
			}
			t.Log(plan.String())
			assert.Contains(t, plan.String(), testCase.wantIndex)
			assert.NotContains(t, plan.String(), "USE TEMP B-TREE FOR ORDER BY")
		})
	}
}

func assertDeploymentColumns(t *testing.T, db *storage.Database, id, errorCode string, phase deploymentmodel.Phase) {
	t.Helper()
	var row struct {
		ErrorCode string                `db:"error_code"`
		Phase     deploymentmodel.Phase `db:"phase"`
		Details   []byte                `db:"details_payload"`
	}
	require.NoError(t, db.Get(t.Context(), &row,
		"SELECT error_code,phase,details_payload FROM deployments WHERE id=?", id))
	assert.Equal(t, errorCode, row.ErrorCode)
	assert.Equal(t, phase, row.Phase)
	var details map[string]any
	require.NoError(t, json.Unmarshal(row.Details, &details))
	for _, key := range []string{"id", "stack", "commit", "status", "error_code", "phase", "started_at", "finished_at"} {
		assert.NotContains(t, details, key)
	}
}

func TestInterruptedTransitionIsAtomicDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	handler, err := history.NewSQLStore(db, 10)
	require.NoError(t, err)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployInterrupted, "interrupted-test", handler))
	service := NewService(db, bus)
	attempt, err := service.StartIfChanged(ctx, "app", "commit", desired("api:1"), true)
	require.NoError(t, err)
	require.NoError(t, service.InterruptRunning(ctx))
	require.NoError(t, service.InterruptRunning(ctx))
	current, err := deploymentstore.NewStore(db).Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Interrupted, current.Status)
	runtime, err := modelstore.NewSQLStore(db).Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "process_interrupted", runtime.Stacks["app"].LastError)
	var eventsCount int
	require.NoError(t, db.Get(ctx, &eventsCount,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", events.TypeNameDeployInterrupted))
	assert.Equal(t, 1, eventsCount)
	_, err = deploymentstore.NewStore(db).Baseline(ctx, "app")
	assert.ErrorIs(t, err, deploymentstore.ErrNotFound)
}

func TestRollbackRecoverySynchronizesRunningAttemptWithTerminalDeployment(t *testing.T) {
	tests := []struct {
		name              string
		status            deploymentmodel.Status
		errorCode         string
		diagnostic        string
		phase             deploymentmodel.Phase
		apply             deploymentmodel.StageStatus
		verification      deploymentmodel.StageStatus
		cleanup           deploymentmodel.StageStatus
		wantBaselineImage string
	}{
		{
			name: "succeeded", status: deploymentmodel.Succeeded, phase: deploymentmodel.PhaseCompleted,
			apply: deploymentmodel.StageSucceeded, verification: deploymentmodel.StageSkipped, cleanup: deploymentmodel.StageSucceeded,
			wantBaselineImage: "api:2",
		},
		{
			name: "failed", status: deploymentmodel.Failed, errorCode: "apply_failed", diagnostic: "rollback apply failed",
			phase: deploymentmodel.PhaseApply, apply: deploymentmodel.StageFailed, verification: deploymentmodel.StageSkipped, cleanup: deploymentmodel.StageSkipped,
			wantBaselineImage: "api:1",
		},
		{
			name: "interrupted", status: deploymentmodel.Interrupted, errorCode: "verification_unknown",
			diagnostic: "rollback verification interrupted", phase: deploymentmodel.PhaseVerification,
			apply: deploymentmodel.StageSucceeded, verification: deploymentmodel.StageUnknown, cleanup: deploymentmodel.StageSkipped,
			wantBaselineImage: "api:1",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			db, err := storage.Open(ctx, dir)
			require.NoError(t, err)
			bus := eventbus.NewOutboxDispatcher(outbox.New(db))
			handler, err := history.NewSQLStore(db, 10)
			require.NoError(t, err)
			for _, eventType := range []events.TypeName{
				events.TypeNameDeploySuccess, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted,
			} {
				require.NoError(t, bus.Subscribe(eventType, "rollback-"+string(eventType), handler))
			}

			service := NewService(db, bus)
			baselineAttempt, err := service.StartIfChanged(ctx, "app", "baseline", desired("api:1"), true)
			require.NoError(t, err)
			require.NoError(t, service.Succeed(ctx, baselineAttempt.ID, model.Stack{}))
			attempt, err := service.StartIfChanged(ctx, "app", "target", desired("api:2"), true)
			require.NoError(t, err)
			require.NotNil(t, attempt)

			store := deploymentstore.NewStore(db)
			parent, err := store.Get(ctx, attempt.DeploymentID)
			require.NoError(t, err)
			finishedAt := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
			parent.Status, parent.FinishedAt = testCase.status, &finishedAt
			parent.ErrorCode, parent.ErrorDiagnostic = testCase.errorCode, testCase.diagnostic
			parent.Phase, parent.ApplyStatus = testCase.phase, testCase.apply
			parent.VerificationStatus, parent.CleanupStatus = testCase.verification, testCase.cleanup
			parent.ActualStateStatus, parent.ObservedAt = deploymentmodel.ActualStateUnknown, nil
			details := legacyDeploymentDetailsPayload(t, parent)
			require.NoError(t, err)
			_, err = db.Exec(ctx, `UPDATE deployments
				SET status=?, error_code=?, phase=?, finished_at=?, details_payload=? WHERE id=?`,
				parent.Status, parent.ErrorCode, parent.Phase, finishedAt, details, parent.ID)
			require.NoError(t, err)
			if testCase.status == deploymentmodel.Succeeded {
				require.NoError(t, store.SaveBaseline(ctx, parent.ID))
			}

			desiredState, err := store.Desired(ctx, parent.ID)
			require.NoError(t, err)
			meta := events.DeployEvent{
				DeploymentID: parent.ID, StackName: parent.Stack, Commit: parent.Commit,
				Services: desiredState.Compose.Services, StackDefinition: desiredState,
			}
			var oldCompletion events.Event
			switch testCase.status {
			case deploymentmodel.Succeeded:
				oldCompletion = &events.DeploySuccess{DeployEvent: meta}
			case deploymentmodel.Failed:
				oldCompletion = &events.DeployFailed{
					DeployEvent: meta, Error: fmt.Errorf("%s", testCase.diagnostic),
					ErrorCode: testCase.errorCode, Stage: string(testCase.phase),
				}
			case deploymentmodel.Interrupted:
				oldCompletion = &events.DeployInterrupted{
					DeploymentID: parent.ID, StackName: parent.Stack, Commit: parent.Commit,
					Services: desiredState.Compose.Services, Error: fmt.Errorf("%s", testCase.diagnostic),
					Reason: testCase.errorCode, Stage: string(testCase.phase),
				}
			default:
				t.Fatalf("unsupported test status %q", testCase.status)
			}
			require.NoError(t, bus.Publish(ctx, oldCompletion))

			var before struct {
				Status         deploymentmodel.Status `db:"status"`
				ErrorCode      string                 `db:"error_code"`
				Phase          deploymentmodel.Phase  `db:"phase"`
				FinishedAt     time.Time              `db:"finished_at"`
				DetailsPayload []byte                 `db:"details_payload"`
			}
			require.NoError(t, db.Get(ctx, &before, `SELECT status, error_code, phase, finished_at,
				details_payload FROM deployments WHERE id=?`, parent.ID))
			var eventsBefore int
			require.NoError(t, db.Get(ctx, &eventsBefore, "SELECT count(*) FROM outbox_events"))
			require.NoError(t, db.Close())

			db, err = storage.Open(ctx, dir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			bus = eventbus.NewOutboxDispatcher(outbox.New(db))
			handler, err = history.NewSQLStore(db, 10)
			require.NoError(t, err)
			for _, eventType := range []events.TypeName{
				events.TypeNameDeploySuccess, events.TypeNameDeployFailed, events.TypeNameDeployInterrupted,
			} {
				require.NoError(t, bus.Subscribe(eventType, "rollback-"+string(eventType), handler))
			}

			for range 2 {
				recoveredService := NewService(db, bus)
				require.NoError(t, recoveredService.InterruptRunning(ctx))
			}

			recovered, err := deploymentstore.NewStore(db).Get(ctx, parent.ID)
			require.NoError(t, err)
			assert.Equal(t, testCase.status, recovered.Status)
			require.Len(t, recovered.Attempts, 1)
			recoveredAttempt := recovered.Attempts[0]
			assert.Equal(t, testCase.status, recoveredAttempt.Status)
			assert.Equal(t, testCase.errorCode, recoveredAttempt.ErrorCode)
			assert.Equal(t, testCase.diagnostic, recoveredAttempt.ErrorDiagnostic)
			assert.Equal(t, testCase.phase, recoveredAttempt.Phase)
			assert.Equal(t, testCase.apply, recoveredAttempt.ApplyStatus)
			assert.Equal(t, testCase.verification, recoveredAttempt.VerificationStatus)
			assert.Equal(t, testCase.cleanup, recoveredAttempt.CleanupStatus)
			require.NotNil(t, recoveredAttempt.FinishedAt)
			assert.Equal(t, finishedAt, *recoveredAttempt.FinishedAt)

			var after struct {
				Status         deploymentmodel.Status `db:"status"`
				ErrorCode      string                 `db:"error_code"`
				Phase          deploymentmodel.Phase  `db:"phase"`
				FinishedAt     time.Time              `db:"finished_at"`
				DetailsPayload []byte                 `db:"details_payload"`
			}
			require.NoError(t, db.Get(ctx, &after, `SELECT status, error_code, phase, finished_at,
				details_payload FROM deployments WHERE id=?`, parent.ID))
			assert.Equal(t, before, after, "recovery must preserve the old binary's deployment outcome")
			baseline, err := deploymentstore.NewStore(db).Baseline(ctx, "app")
			require.NoError(t, err)
			assert.Equal(t, testCase.wantBaselineImage, baseline.Definition.Compose.Services[0].Image)
			var eventsAfter int
			require.NoError(t, db.Get(ctx, &eventsAfter, "SELECT count(*) FROM outbox_events"))
			assert.Equal(t, eventsBefore, eventsAfter, "recovery must not publish a duplicate outcome")
		})
	}
}

func TestDeploymentPayloadsDoNotDuplicateSQLColumns(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	attempt, err := service.StartIfChanged(ctx, "app", "commit", desired("api:1"), true)
	require.NoError(t, err)
	store := deploymentstore.NewStore(db)
	stored, err := store.Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, attempt.ID, stored.LatestAttemptID)
	assert.Equal(t, 1, stored.AttemptCount)
	require.Len(t, stored.Attempts, 1)
	assert.Equal(t, *attempt, stored.Attempts[0])
	assertDeploymentColumns(t, db, attempt.DeploymentID, "", deploymentmodel.PhaseApply)
}

func TestSnapshotMaskingAndSemanticChanges(t *testing.T) {
	beforeFile := desired("api:1")
	beforeFile.Compose.Services[0].Command = compose.NewCommand([]string{"sh", "-c", "exec app --password 'review-secret-value'"})
	beforeFile.Compose.Services[0].Healthcheck = &compose.ServiceHealth{
		Test: compose.NewCommand([]string{"CMD-SHELL", "curl -u healthcheck-secret localhost"}),
	}
	beforeFile.Compose.Services[0].InitJobs = []compose.InitJob{{
		Name: "migrate", Image: "api:1", Entrypoint: []string{"sh", "-c"}, Command: []string{"migrate --token init-secret"},
	}}
	beforeFile.Compose.Configs = compose.Configs{"cfg": {Data: []byte("raw-config-secret"), File: "app.cfg"}}
	before, err := deploymentmodel.Prepare(beforeFile)
	require.NoError(t, err)
	payload, err := json.Marshal(before)
	require.NoError(t, err)
	for _, secret := range []string{
		"plaintext-secret", "review-secret-value", "healthcheck-secret", "init-secret", "raw-config-secret",
	} {
		assert.NotContains(t, string(payload), secret)
	}
	assert.Equal(t, "plaintext-secret", beforeFile.Compose.Services[0].Environment.Map["API_TOKEN"], "preparation must not mutate apply input")
	afterFile := desired("api:2")
	afterFile.Compose.Services[0].Environment.Map["API_TOKEN"] = "new-secret"
	afterFile.Compose.Services[0].Environment.Map["MODE"] = "staging"
	after, err := deploymentmodel.Prepare(afterFile)
	require.NoError(t, err)
	changes := deploymentmodel.Compare(before, after)
	byField := map[string]deploymentmodel.Change{}
	for _, change := range changes {
		byField[change.ResourceType+"/"+change.ResourceName+"/"+change.Field] = change
	}
	image := byField["service/api/image"]
	require.NotNil(t, image.Before)
	assert.Equal(t, "api:1", *image.Before)
	assert.Equal(t, "api:2", *image.After)
	mode := byField["service/api/environment.MODE"]
	require.NotNil(t, mode.After)
	assert.Equal(t, "staging", *mode.After)
	token := byField["service/api/environment.API_TOKEN"]
	require.NotNil(t, token.After)
	assert.True(t, token.Redacted)
	assert.Equal(t, "****", *token.After)
	// Source identity and parser ordering do not create effective changes.
	afterFile.Path = "/different/checkout/app.yaml"
	afterFile.Digest = "different-source-format"
	afterFile.Compose.Services[0].Environment.Keys = []string{"MODE", "API_TOKEN"}
	same, err := deploymentmodel.Prepare(afterFile)
	require.NoError(t, err)
	assert.Equal(t, after.Digest, same.Digest)
}

func TestServiceNetworkFieldsDoNotCollide(t *testing.T) {
	beforeFile := desired("api:1")
	beforeFile.Compose.Services[0].Networks = compose.NewServiceNetworks(&compose.ServiceNetwork{
		Alias: "infra", ResolvedName: "infra", IPV4Address: "10.0.0.2",
	})
	afterFile := desired("api:1")
	afterFile.Compose.Services[0].Networks = compose.NewServiceNetworks(&compose.ServiceNetwork{
		Alias: "infra", ResolvedName: "wmb-infra", IPV4Address: "10.0.0.3",
	})
	before, err := deploymentmodel.Prepare(beforeFile)
	require.NoError(t, err)
	after, err := deploymentmodel.Prepare(afterFile)
	require.NoError(t, err)
	changes := deploymentmodel.Compare(before, after)
	fields := map[string]bool{}
	for _, change := range changes {
		key := change.ResourceType + "/" + change.ResourceName + "/" + change.Field
		assert.False(t, fields[key], "duplicate public change key %s", key)
		fields[key] = true
	}
	assert.True(t, fields["service/api/networks[infra].resolved_name"])
	assert.True(t, fields["service/api/networks[infra].ipv4_address"])
}

func TestOpaqueCommandChangesRemainDetectable(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*compose.Service, string)
	}{
		{name: "command", mutate: func(service *compose.Service, secret string) {
			service.Command = compose.NewCommand([]string{"sh", "-c", "app --password " + secret})
		}},
		{name: "entrypoint", mutate: func(service *compose.Service, secret string) {
			service.Extra = map[string]any{"entrypoint": []string{"sh", "-c", "bootstrap --token " + secret}}
		}},
		{name: "healthcheck", mutate: func(service *compose.Service, secret string) {
			service.Healthcheck = &compose.ServiceHealth{Test: compose.NewCommand([]string{"CMD-SHELL", "check --token " + secret})}
		}},
		{name: "init job", mutate: func(service *compose.Service, secret string) {
			service.InitJobs = []compose.InitJob{{Name: "init", Image: "api:1", Entrypoint: []string{"sh", "-c"}, Command: []string{"init --token " + secret}}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeFile, afterFile := desired("api:1"), desired("api:1")
			tt.mutate(&beforeFile.Compose.Services[0], "old-secret")
			tt.mutate(&afterFile.Compose.Services[0], "new-secret")
			before, err := deploymentmodel.Prepare(beforeFile)
			require.NoError(t, err)
			after, err := deploymentmodel.Prepare(afterFile)
			require.NoError(t, err)
			assert.NotEqual(t, before.Digest, after.Digest)
			changes := deploymentmodel.Compare(before, after)
			require.NotEmpty(t, changes)
			encoded, err := json.Marshal(struct {
				Before  deploymentmodel.Prepared
				After   deploymentmodel.Prepared
				Changes []deploymentmodel.Change
			}{before, after, changes})
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "old-secret")
			assert.NotContains(t, string(encoded), "new-secret")
		})
	}
}

func TestCommandRepresentationAffectsEffectiveDigest(t *testing.T) {
	parse := func(t *testing.T, command string) compose.File {
		t.Helper()
		parsed, err := compose.Parse([]byte("services:\n  api:\n    image: api:1\n    command: " + command + "\n"))
		require.NoError(t, err)
		return compose.File{Compose: *parsed}
	}
	scalar, err := deploymentmodel.Prepare(parse(t, "echo hello"))
	require.NoError(t, err)
	list, err := deploymentmodel.Prepare(parse(t, "[\"echo hello\"]"))
	require.NoError(t, err)
	assert.NotEqual(t, scalar.Digest, list.Digest)

	formatted, err := deploymentmodel.Prepare(parse(t, "'echo hello'"))
	require.NoError(t, err)
	assert.Equal(t, scalar.Digest, formatted.Digest)
}

func TestCommandSemanticsDriveDeploymentAttempts(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	parse := func(command string) compose.File {
		parsed, parseErr := compose.Parse([]byte("services:\n  api:\n    image: api:1\n    command: " + command + "\n"))
		require.NoError(t, parseErr)
		return compose.File{Compose: *parsed}
	}
	baseline, err := service.StartIfChanged(ctx, "app", "one", parse("echo hello"), true)
	require.NoError(t, err)
	require.NoError(t, service.Succeed(ctx, baseline.ID, model.Stack{}))
	formatOnly, err := service.StartIfChanged(ctx, "app", "two", parse("'echo hello'"), true)
	require.NoError(t, err)
	assert.Nil(t, formatOnly)
	changed, err := service.StartIfChanged(ctx, "app", "three", parse("[\"echo hello\"]"), true)
	require.NoError(t, err)
	require.NotNil(t, changed)
	assert.NotEqual(t, baseline.DeploymentID, changed.DeploymentID)
}

func TestNoBaselineNeverClaimsObservedState(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	service := NewService(db, eventbus.NewOutboxDispatcher(outbox.New(db)))
	attempt, err := service.StartIfChanged(ctx, "app", "first", desired("api:1"), true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	initial, err := deploymentstore.NewStore(db).Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.BasisNone, initial.ComparisonBasis)
	assert.Equal(t, deploymentmodel.ComparisonUnknown, initial.ComparisonStatus)
	require.NoError(t, service.SucceedObserved(ctx, attempt.ID, model.Stack{}, time.Now()))
	stored, err := deploymentstore.NewStore(db).Get(ctx, attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.StageSkipped, stored.VerificationStatus)
	assert.Equal(t, deploymentmodel.ActualStateObserved, stored.ActualStateStatus)
	assert.Equal(t, deploymentmodel.ComparisonUnknown, stored.ComparisonStatus)
}

func TestIndependentCollectionChangesAndReorder(t *testing.T) {
	parse := func(ports, volumes string) deploymentmodel.Prepared {
		parsed, err := compose.Parse([]byte("services:\n  api:\n    image: api:1\n    ports:\n" + ports + "    volumes:\n" + volumes))
		require.NoError(t, err)
		result, err := deploymentmodel.Prepare(compose.File{Compose: *parsed})
		require.NoError(t, err)
		return result
	}
	before := parse("      - '8080:80'\n      - '8081:81'\n",
		"      - 'db:/data'\n      - 'cache:/cache'\n")
	after := parse("      - '9091:81'\n      - '9090:80'\n",
		"      - 'cache2:/cache'\n      - 'db2:/data'\n")
	changes := deploymentmodel.Compare(before, after)
	fields := map[string]deploymentmodel.Change{}
	for _, c := range changes {
		fields[c.Field] = c
	}
	for field, values := range map[string][2]string{
		"ports[80/tcp].published": {"8080", "9090"},
		"ports[81/tcp].published": {"8081", "9091"},
		"volumes[/data].source":   {"db", "db2"},
		"volumes[/cache].source":  {"cache", "cache2"},
	} {
		change, ok := fields[field]
		require.True(t, ok, "missing %s: %#v", field, changes)
		require.NotNil(t, change.Before)
		require.NotNil(t, change.After)
		assert.Equal(t, values[0], *change.Before)
		assert.Equal(t, values[1], *change.After)
		assert.Equal(t, deploymentmodel.OperationChanged, change.Operation)
	}
	assert.Equal(t, 4, len(changes), "independent transitions must not merge")
	assert.Equal(t, before.Digest, parse("      - '8081:81'\n      - '8080:80'\n",
		"      - 'cache:/cache'\n      - 'db:/data'\n").Digest)
	added := parse("      - '8080:80'\n      - '8082:82'\n",
		"      - 'db:/data'\n")
	transitions := deploymentmodel.Compare(before, added)
	var sawAdded, sawRemoved bool
	for _, c := range transitions {
		if c.Operation == deploymentmodel.OperationAdded && strings.Contains(c.Field, "[82/tcp]") {
			sawAdded = true
		}
		if c.Operation == deploymentmodel.OperationRemoved && strings.Contains(c.Field, "[81/tcp]") {
			sawRemoved = true
		}
	}
	assert.True(t, sawAdded)
	assert.True(t, sawRemoved)
}
