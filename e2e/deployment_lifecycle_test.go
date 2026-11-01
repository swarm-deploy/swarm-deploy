package e2e

import (
	"strings"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmInitialStartupFailureHistoryAndRecovery(t *testing.T) {
	f := newLifecycleFixture(t, true)
	// A never-healthy startup check holds the task in Starting until exit 23,
	// preventing a transient Running observation from racing verification.
	broken := strings.Replace(lifecycleService("job", "sleep 1; exit 23", ""), "    deploy:\n",
		"    healthcheck: {test: [CMD, 'false'], interval: 1s, timeout: 1s, retries: 30}\n    deploy:\n", 1)
	desired := f.manifest(t, broken)
	req := deployments.ExecuteRequest{Stack: f.request.Stack, Desired: desired, Commit: "broken", SourceChanged: true}
	// Unlike a failed rolling update, initial startup has no UpdateStatus. A
	// real failed task with restart disabled is still a known convergence failure.
	result, err := f.executor.Execute(f.ctx, req)
	require.Error(t, err)
	require.NotNil(t, result.Attempt)
	service := f.inspect(t, "job")
	require.NotEmpty(t, service.ID, "ServiceCreate succeeded before task startup failed")
	require.Nil(t, service.UpdateStatus, "this must exercise initial creation, not a rolling update")
	var initialTask string
	f.poll(t, "real initial task exited 23", func() bool {
		for _, task := range f.tasks(t, "job") {
			if task.CurrentState == swarm.TaskStateFailed && task.ExitCode != nil && *task.ExitCode == 23 {
				initialTask = task.ID
				return true
			}
		}
		return false
	})
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	failed := f.latest(t)
	assert.Equal(t, model.Failed, result.Status)
	assert.Equal(t, "convergence_failed", result.ErrorCode)
	assertVerificationFailure(t, failed, model.Failed, "convergence_failed", model.StageFailed)
	assert.False(t, failed.Retryable, "known terminal failures must not retry automatically")
	assert.Contains(t, failed.ErrorDiagnostic, "state=failed")
	assert.Contains(t, failed.ErrorDiagnostic, "exit_code=23")
	assert.Contains(t, failed.ErrorDiagnostic, "id=")
	require.Len(t, f.publications(t, events.TypeNameDeployFailed), 1)
	assertFailedPublication(t, f, failed)
	assertDeferredExecutions(t, f, req, failed.ID, 1)

	req.ManualRetry = true
	retried, err := f.executor.Execute(f.ctx, req)
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	require.NotNil(t, retried.Attempt)
	assert.Equal(t, failed.ID, retried.Attempt.DeploymentID)
	assert.Equal(t, 2, retried.Attempt.Number)
	retriedService := f.inspect(t, "job")
	assert.Equal(t, service.ID, retriedService.ID)
	assert.Equal(t, service.Spec.TaskTemplate.ForceUpdate+1, retriedService.Spec.TaskTemplate.ForceUpdate)
	f.poll(t, "explicit retry executes a new real task", func() bool {
		for _, task := range f.tasks(t, "job") {
			if task.ID != initialTask && task.CurrentState == swarm.TaskStateFailed && task.ExitCode != nil && *task.ExitCode == 23 {
				return true
			}
		}
		return false
	})
	again := f.latest(t)
	require.Len(t, again.Attempts, 2)
	assert.Equal(t, model.Failed, again.Attempts[0].Status)
	assert.Equal(t, model.Failed, again.Attempts[1].Status)
	assertFailedPublication(t, f, again)
	assertDeferredExecutions(t, f, reqWithAutomaticRetry(req), failed.ID, 2)

	req.ManualRetry, req.Commit = false, "working"
	req.Desired = f.manifest(t, lifecycleService("job", "sleep 600", ""))
	recovered, err := f.executor.Execute(f.ctx, req)
	require.NoError(t, err)
	assert.Equal(t, model.Succeeded, recovered.Status)
	f.runningIDs(t, "job", 1)
	assertSuccessfulHistory(t, f.latest(t))
	prior, err := f.store.Get(f.ctx, failed.ID)
	require.NoError(t, err)
	require.Len(t, prior.Attempts, 2)
	assert.Equal(t, model.Failed, prior.Attempts[1].Status, "recovery preserves failed execution history")
	f.delivered(t, events.TypeNameDeploySuccess, 1)
}

func TestRealSwarmNativeRollbackHistoryRetryAndRecovery(t *testing.T) {
	f := newLifecycleFixture(t, true)
	working := lifecycleService("job", "sleep 600", "")
	// Both update and rollback monitoring are explicit; no task restart can
	// conceal the nonzero exit that triggers Swarm's native rollback.
	working = strings.Replace(working, "failure_action: pause", "failure_action: rollback", 1)
	working += "      rollback_config: {monitor: 1s, failure_action: pause}\n"
	f.manifest(t, working)
	f.reconcile(t)
	originalIDs := f.runningIDs(t, "job", 1)
	original := f.settled(t, "job")
	baseline := f.latest(t)
	assertSuccessfulHistory(t, baseline)

	f.manifest(t, strings.Replace(working, "sleep 600", "exit 31", 1))
	f.request.Commit = "rollback"
	err := f.reconciler.Reconcile(f.ctx, f.request)
	require.Error(t, err)
	failed := f.latest(t)
	assertVerificationFailure(t, failed, model.Failed, "convergence_failed", model.StageFailed)
	assert.False(t, failed.Retryable)
	assert.Contains(t, failed.ErrorDiagnostic, "rollback")
	f.poll(t, "Swarm completed native rollback", func() bool {
		service := f.inspect(t, "job")
		return service.UpdateStatus != nil && service.UpdateStatus.State == dockerswarm.UpdateStateRollbackCompleted
	})
	rolledBackIDs := f.runningIDs(t, "job", 1)
	rolledBack := f.settled(t, "job")
	assert.Equal(t, original.ID, rolledBack.ID)
	assert.Equal(t, original.Spec.TaskTemplate.ContainerSpec, rolledBack.Spec.TaskTemplate.ContainerSpec)
	assert.NotEqual(t, originalIDs, rolledBackIDs, "rollback actually replaced the failed generation")
	// Swarm clears PreviousSpec on rollback. Persisted diagnostics were captured
	// from the real failing task before task-history garbage collection.
	assert.Contains(t, failed.ErrorDiagnostic, "exit_code=31", "the failing target actually ran")
	assert.Contains(t, failed.ErrorDiagnostic, "state=failed")
	assert.NotContains(t, rolledBack.Spec.TaskTemplate.ContainerSpec.Args, "exit 31")
	// The rollback outcome can be observed before or after completion by the
	// verifier. History must remain failed even once the old version is healthy.
	for range 3 {
		f.reconcile(t)
		current := f.latest(t)
		assert.Equal(t, failed.ID, current.ID)
		assert.Equal(t, 1, current.AttemptCount)
		assert.Equal(t, model.Failed, current.Status)
		assert.Equal(t, rolledBack.Version, f.inspect(t, "job").Version)
		assert.Equal(t, rolledBack.Spec, f.inspect(t, "job").Spec)
	}
	require.Len(t, f.publications(t, events.TypeNameDeployFailed), 1)
	assertFailedPublication(t, f, failed)
	require.Len(t, f.publications(t, events.TypeNameDeploySuccess), 1, "rollback cannot publish success for desired exit 31")

	f.request.IsManual = true
	require.Error(t, f.reconciler.Reconcile(f.ctx, f.request))
	f.request.IsManual = false
	retried := f.latest(t)
	assert.Equal(t, failed.ID, retried.ID)
	assert.Equal(t, 2, retried.AttemptCount)
	assertVerificationFailure(t, retried, model.Failed, "convergence_failed", model.StageFailed)
	assertFailedPublication(t, f, retried)
	f.poll(t, "retry rollback completed", func() bool {
		service := f.inspect(t, "job")
		return service.UpdateStatus != nil && service.UpdateStatus.State == dockerswarm.UpdateStateRollbackCompleted
	})
	f.request.Commit = "valid-update"
	f.manifest(t, strings.Replace(working, "sleep 600", "echo recovered; sleep 600", 1))
	f.reconcile(t)
	f.runningIDs(t, "job", 1)
	assert.Contains(t, f.inspect(t, "job").Spec.TaskTemplate.ContainerSpec.Args, "echo recovered; sleep 600")
	assertSuccessfulHistory(t, f.latest(t))
	f.delivered(t, events.TypeNameDeployFailed, 2)
	f.delivered(t, events.TypeNameDeploySuccess, 2)
}

func assertVerificationFailure(t *testing.T, item model.Deployment, status model.Status, code string, verification model.StageStatus) {
	t.Helper()
	require.NotEmpty(t, item.Attempts)
	assert.Equal(t, status, item.Status)
	assert.Equal(t, code, item.ErrorCode)
	assert.Equal(t, model.PhaseVerification, item.Phase)
	assert.Equal(t, model.StageSucceeded, item.ApplyStatus)
	assert.Equal(t, verification, item.VerificationStatus)
	assert.Equal(t, model.StageSkipped, item.CleanupStatus)
	assert.True(t, item.Verification.Known)
	assert.Equal(t, []string{"job"}, item.Verification.UnresolvedServices)
	attempt := item.Attempts[0]
	assert.Equal(t, status, attempt.Status)
	assert.Equal(t, code, attempt.ErrorCode)
	assert.Equal(t, item.ErrorDiagnostic, attempt.ErrorDiagnostic)
	assert.NotEmpty(t, attempt.ErrorDiagnostic)
	assert.Equal(t, item.Phase, attempt.Phase)
	assert.Equal(t, item.ApplyStatus, attempt.ApplyStatus)
	assert.Equal(t, item.VerificationStatus, attempt.VerificationStatus)
	assert.Equal(t, item.CleanupStatus, attempt.CleanupStatus)
	assert.NotNil(t, attempt.FinishedAt)
}

func assertSuccessfulHistory(t *testing.T, item model.Deployment) {
	t.Helper()
	assert.Equal(t, model.Succeeded, item.Status)
	assert.Equal(t, model.PhaseCompleted, item.Phase)
	assert.Empty(t, item.ErrorCode)
	assert.True(t, item.Verification.Known)
	assert.Empty(t, item.Verification.UnresolvedServices)
	require.NotEmpty(t, item.Attempts)
	assert.Equal(t, model.Succeeded, item.Attempts[0].Status)
	assert.Empty(t, item.Attempts[0].ErrorDiagnostic)
}

func assertDeferredExecutions(t *testing.T, f *lifecycleFixture, req deployments.ExecuteRequest, id string, attempts int) {
	t.Helper()
	before := f.inspect(t, "job")
	for range 3 {
		result, err := f.executor.Execute(f.ctx, req)
		require.NoError(t, err)
		assert.True(t, result.RetryDeferred)
		assert.Nil(t, result.Attempt)
		current := f.latest(t)
		assert.Equal(t, id, current.ID)
		assert.Equal(t, attempts, current.AttemptCount)
		assert.Equal(t, before.Spec, f.inspect(t, "job").Spec)
		assert.Equal(t, before.Version, f.inspect(t, "job").Version)
	}
}

func reqWithAutomaticRetry(req deployments.ExecuteRequest) deployments.ExecuteRequest {
	req.ManualRetry = false
	return req
}

func assertFailedPublication(t *testing.T, f *lifecycleFixture, item model.Deployment) {
	t.Helper()
	publications := f.publications(t, events.TypeNameDeployFailed)
	require.NotEmpty(t, publications)
	event, ok := publications[len(publications)-1].Envelope.Event.(*events.DeployFailed)
	require.True(t, ok)
	assert.Equal(t, item.ID, event.DeploymentID)
	assert.Equal(t, item.LatestAttemptID, event.AttemptID)
	assert.Equal(t, item.ErrorCode, event.ErrorCode)
	assert.Equal(t, string(model.PhaseVerification), event.Stage)
	require.Error(t, event.Error)
	assert.Equal(t, item.ErrorDiagnostic, event.Error.Error())
}
