package deployments

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestCompletePersistsLifecycleBaselineAndEvents(t *testing.T) {
	observedAt := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		completion       Completion
		wantStatus       deploymentmodel.Status
		wantPhase        deploymentmodel.Phase
		wantApply        deploymentmodel.StageStatus
		wantVerification deploymentmodel.StageStatus
		wantCleanup      deploymentmodel.StageStatus
		wantActual       deploymentmodel.ActualStateStatus
		wantEvent        events.TypeName
		wantEventStage   string
	}{
		{
			name: "success without observation", completion: Completion{Outcome: CompletionSucceeded},
			wantStatus: deploymentmodel.Succeeded, wantPhase: deploymentmodel.PhaseCompleted,
			wantApply: deploymentmodel.StageSucceeded, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSucceeded, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeploySuccess,
		},
		{
			name:       "success with observation",
			completion: Completion{Outcome: CompletionSucceeded, ObservedAt: &observedAt},
			wantStatus: deploymentmodel.Succeeded, wantPhase: deploymentmodel.PhaseCompleted,
			wantApply: deploymentmodel.StageSucceeded, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSucceeded, wantActual: deploymentmodel.ActualStateObserved,
			wantEvent: events.TypeNameDeploySuccess,
		},
		{
			name: "apply failure",
			completion: Completion{Outcome: CompletionApplyFailed,
				Diagnostic: errors.New("docker failed: token=private-value")},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply,
			wantApply: deploymentmodel.StageFailed, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSkipped, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeployFailed, wantEventStage: "apply",
		},
		{
			name:       "policy rejection",
			completion: Completion{Outcome: CompletionPolicyRejected},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply,
			wantApply: deploymentmodel.StageFailed, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSkipped, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeployFailed, wantEventStage: "apply",
		},
		{
			name:       "init job failure",
			completion: Completion{Outcome: CompletionInitJobFailed},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseApply,
			wantApply: deploymentmodel.StageFailed, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSkipped, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeployFailed, wantEventStage: "apply",
		},
		{
			name: "verification interruption",
			completion: Completion{Outcome: CompletionVerificationInterrupted,
				Diagnostic: errors.New("inspect failed: password=private-value")},
			wantStatus: deploymentmodel.Interrupted, wantPhase: deploymentmodel.PhaseVerification,
			wantApply: deploymentmodel.StageSucceeded, wantVerification: deploymentmodel.StageUnknown,
			wantCleanup: deploymentmodel.StageSkipped, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeployInterrupted, wantEventStage: "verification",
		},
		{
			name: "cleanup failure",
			completion: Completion{Outcome: CompletionCleanupFailed,
				Diagnostic: errors.New("prune failed: token=private-value"), ObservedAt: &observedAt},
			wantStatus: deploymentmodel.Failed, wantPhase: deploymentmodel.PhaseCleanup,
			wantApply: deploymentmodel.StageSucceeded, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageFailed, wantActual: deploymentmodel.ActualStateObserved,
			wantEvent: events.TypeNameDeployFailed, wantEventStage: "cleanup",
		},
		{
			name:       "process interruption",
			completion: Completion{Outcome: CompletionProcessInterrupted},
			wantStatus: deploymentmodel.Interrupted, wantPhase: deploymentmodel.PhaseApply,
			wantApply: deploymentmodel.StageUnknown, wantVerification: deploymentmodel.StageSkipped,
			wantCleanup: deploymentmodel.StageSkipped, wantActual: deploymentmodel.ActualStateUnknown,
			wantEvent: events.TypeNameDeployInterrupted,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clock := time.Date(2026, time.October, 30, 10, 0, 0, 0, time.UTC)
			db, service, bus := openService(t, WithClock(func() time.Time {
				clock = clock.Add(time.Second)
				return clock
			}))
			subscribe(t, db, bus, testCase.wantEvent)
			attempt, err := service.StartIfChanged(
				t.Context(), "app", "commit", desiredServices("api"), true, nil,
			)
			require.NoError(t, err)
			projected := false
			require.NoError(t, service.Complete(t.Context(), attempt.ID, testCase.completion,
				func(_ context.Context, deployment deploymentmodel.Deployment) error {
					projected = true
					assert.Equal(t, attempt.DeploymentID, deployment.ID)
					return nil
				}))
			assert.True(t, projected)

			stored, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantStatus, stored.Status)
			assert.Equal(t, testCase.wantPhase, stored.Phase)
			assert.Equal(t, testCase.wantApply, stored.ApplyStatus)
			assert.Equal(t, testCase.wantVerification, stored.VerificationStatus)
			assert.Equal(t, testCase.wantCleanup, stored.CleanupStatus)
			assert.Equal(t, testCase.wantActual, stored.ActualStateStatus)
			assert.Equal(t, testCase.completion.ObservedAt, stored.ObservedAt)
			require.NotNil(t, stored.FinishedAt)
			assert.False(t, stored.FinishedAt.Before(stored.StartedAt))
			require.Len(t, stored.Attempts, 1)
			assert.Equal(t, stored.Status, stored.Attempts[0].Status)
			assert.Equal(t, stored.Phase, stored.Attempts[0].Phase)
			assert.NotContains(t, stored.ErrorDiagnostic, "private-value")

			assertCompletionPublication(t, db, testCase.wantEvent, attempt, testCase.completion, testCase.wantEventStage)
			baseline, baselineErr := deploymentstore.NewStore(db).BaselineDeploymentID(t.Context(), "app")
			if testCase.wantStatus == deploymentmodel.Succeeded {
				require.NoError(t, baselineErr)
				assert.Equal(t, attempt.DeploymentID, baseline)
			} else {
				assert.ErrorIs(t, baselineErr, deploymentstore.ErrNotFound)
			}
		})
	}
}

func assertCompletionPublication(
	t *testing.T,
	db storage.Database,
	wantType events.TypeName,
	attempt *deploymentmodel.Attempt,
	completion Completion,
	wantStage string,
) {
	t.Helper()
	items := publications(t, db, wantType)
	require.Len(t, items, 1)
	event := items[0].Envelope.Event
	switch wantType {
	case events.TypeNameDeploySuccess:
		actual, ok := event.(*events.DeploySuccess)
		require.True(t, ok)
		assert.Equal(t, attempt.DeploymentID, actual.DeploymentID)
		assert.Equal(t, attempt.ID, actual.AttemptID)
		assert.Equal(t, "app", actual.StackName)
		assert.Equal(t, "commit", actual.Commit)
		require.Len(t, actual.Services, 1)
	case events.TypeNameDeployFailed:
		actual, ok := event.(*events.DeployFailed)
		require.True(t, ok)
		assert.Equal(t, attempt.DeploymentID, actual.DeploymentID)
		assert.Equal(t, attempt.ID, actual.AttemptID)
		assert.Equal(t, "app", actual.StackName)
		assert.Equal(t, "commit", actual.Commit)
		require.Len(t, actual.Services, 1)
		assert.Equal(t, completion.ErrorCode(), actual.ErrorCode)
		assert.Equal(t, wantStage, actual.Stage)
		if actual.Error != nil {
			assert.NotContains(t, actual.Error.Error(), "private-value")
		}
	case events.TypeNameDeployInterrupted:
		actual, ok := event.(*events.DeployInterrupted)
		require.True(t, ok)
		assert.Equal(t, attempt.DeploymentID, actual.DeploymentID)
		assert.Equal(t, attempt.ID, actual.AttemptID)
		assert.Equal(t, "app", actual.StackName)
		assert.Equal(t, "commit", actual.Commit)
		require.Len(t, actual.Services, 1)
		assert.Equal(t, completion.ErrorCode(), actual.Reason)
		assert.Equal(t, wantStage, actual.Stage)
		if actual.Error != nil {
			assert.NotContains(t, actual.Error.Error(), "private-value")
		}
	default:
		t.Fatalf("unsupported event type %q", wantType)
	}
}

func TestFailedCompletionDoesNotReplaceSuccessfulBaseline(t *testing.T) {
	db, service, _ := openService(t)
	first, err := service.StartIfChanged(t.Context(), "app", "first", desiredServices("api"), true, nil)
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), first.ID, Completion{Outcome: CompletionSucceeded}, nil))
	second, err := service.StartIfChanged(t.Context(), "app", "second", desiredServices("worker"), true, nil)
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), second.ID, Completion{Outcome: CompletionApplyFailed}, nil))

	baseline, err := deploymentstore.NewStore(db).BaselineDeploymentID(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, first.DeploymentID, baseline)
}

func TestCompleteRejectsInvalidOrTerminalTransitions(t *testing.T) {
	tests := []struct {
		name       string
		completion Completion
	}{
		{name: "running outcome", completion: Completion{}},
		{name: "success with error", completion: Completion{Outcome: CompletionSucceeded, Diagnostic: errors.New("failed")}},
		{name: "unsupported outcome", completion: Completion{Outcome: CompletionOutcome("unknown")}},
		{name: "cleanup without observation", completion: Completion{Outcome: CompletionCleanupFailed}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db, service, _ := openService(t)
			attempt, err := service.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true, nil)
			require.NoError(t, err)
			require.Error(t, service.Complete(t.Context(), attempt.ID, testCase.completion, nil))
			stored, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Running, stored.Status)
			assert.Empty(t, publications(t, db, events.TypeNameDeploySuccess))
			assert.Empty(t, publications(t, db, events.TypeNameDeployFailed))
			assert.Empty(t, publications(t, db, events.TypeNameDeployInterrupted))
		})
	}

	_, service, _ := openService(t)
	first, err := service.StartIfChanged(t.Context(), "app", "first", desiredServices("api"), true, nil)
	require.NoError(t, err)
	require.NoError(t, service.Complete(t.Context(), first.ID, Completion{Outcome: CompletionApplyFailed}, nil))
	retry, err := service.StartIfChanged(t.Context(), "app", "retry", desiredServices("api"), true, nil)
	require.NoError(t, err)
	require.ErrorIs(t, service.Complete(t.Context(), first.ID, Completion{
		Outcome: CompletionApplyFailed,
	}, nil), deploymentstore.ErrNotRunning)
	require.NoError(t, service.Complete(t.Context(), retry.ID, Completion{Outcome: CompletionSucceeded}, nil))
}

func TestCompleteRollsBackEveryTransactionalParticipant(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, db storage.Database) ProjectionUpdater
	}{
		{
			name: "lifecycle persistence failure",
			setup: func(t *testing.T, db storage.Database) ProjectionUpdater {
				_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_attempt_completion BEFORE UPDATE ON deployment_attempts
					WHEN NEW.status <> 'running' BEGIN SELECT RAISE(ABORT, 'attempt persistence failed'); END`)
				require.NoError(t, err)
				return projectionInsert(t, db, nil)
			},
		},
		{
			name: "baseline persistence failure",
			setup: func(t *testing.T, db storage.Database) ProjectionUpdater {
				_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_baseline BEFORE INSERT ON desired_snapshots
					BEGIN SELECT RAISE(ABORT, 'baseline persistence failed'); END`)
				require.NoError(t, err)
				return projectionInsert(t, db, nil)
			},
		},
		{
			name: "projection failure",
			setup: func(t *testing.T, db storage.Database) ProjectionUpdater {
				return projectionInsert(t, db, errors.New("projection failed"))
			},
		},
		{
			name: "event publication failure",
			setup: func(t *testing.T, db storage.Database) ProjectionUpdater {
				_, err := db.Exec(t.Context(), `CREATE TRIGGER reject_completion_event BEFORE INSERT ON outbox_events
					WHEN NEW.event_type='DeploySuccess' BEGIN SELECT RAISE(ABORT, 'event publication failed'); END`)
				require.NoError(t, err)
				return projectionInsert(t, db, nil)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			db, service, bus := openService(t)
			subscribe(t, db, bus, events.TypeNameDeploySuccess)
			_, err := db.Exec(t.Context(), "CREATE TABLE completion_projection (deployment_id TEXT PRIMARY KEY)")
			require.NoError(t, err)
			attempt, err := service.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true, nil)
			require.NoError(t, err)

			project := testCase.setup(t, db)
			require.Error(t, service.Complete(t.Context(), attempt.ID, Completion{Outcome: CompletionSucceeded}, project))

			stored, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Running, stored.Status)
			require.Len(t, stored.Attempts, 1)
			assert.Equal(t, deploymentmodel.Running, stored.Attempts[0].Status)
			_, err = deploymentstore.NewStore(db).BaselineDeploymentID(t.Context(), "app")
			assert.ErrorIs(t, err, deploymentstore.ErrNotFound)
			var projectionCount int
			require.NoError(t, db.Get(t.Context(), &projectionCount, "SELECT COUNT(*) FROM completion_projection"))
			assert.Zero(t, projectionCount)
			assert.Empty(t, publications(t, db, events.TypeNameDeploySuccess))
		})
	}
}

func projectionInsert(t *testing.T, db storage.Database, result error) ProjectionUpdater {
	t.Helper()
	return func(ctx context.Context, deployment deploymentmodel.Deployment) error {
		_, err := db.Exec(ctx, "INSERT INTO completion_projection (deployment_id) VALUES (?)", deployment.ID)
		if err != nil {
			return fmt.Errorf("insert projection: %w", err)
		}
		return result
	}
}

func TestCompleteJoinsExistingTransaction(t *testing.T) {
	db, service, _ := openService(t)
	attempt, err := service.StartIfChanged(t.Context(), "app", "commit", desiredServices("api"), true, nil)
	require.NoError(t, err)

	err = db.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if completeErr := service.Complete(ctx, attempt.ID, Completion{Outcome: CompletionSucceeded}, nil); completeErr != nil {
			return completeErr
		}
		return errors.New("outer transaction failed")
	})
	require.Error(t, err)
	stored, err := deploymentstore.NewStore(db).Get(t.Context(), attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Running, stored.Status)
	assert.Empty(t, publications(t, db, events.TypeNameDeploySuccess))
}
