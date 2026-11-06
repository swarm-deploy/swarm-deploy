package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmJobFailureRetryAndRecovery(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		for _, recovery := range []string{"unchanged-spec-input", "changed-spec"} {
			t.Run(mode+"/"+recovery, func(t *testing.T) {
				f := newLifecycleFixture(t, true)
				input := t.TempDir()
				exitFile := filepath.Join(input, "exit-code")
				require.NoError(t, os.WriteFile(exitFile, []byte("23"), 0o600))
				command := "exit $(cat /input/exit-code)"
				side := ""
				network := fmt.Sprintf("    ipam:\n      config:\n        - subnet: %s\n", f.subnet)
				if external := os.Getenv("SWARM_DEPLOY_E2E_JOB_NETWORK"); external != "" {
					network = fmt.Sprintf("    external: true\n    name: %q\n", external)
				}
				load := func() compose.File {
					manifest := fmt.Sprintf(`services:
  job:
    image: busybox:1.36
    command: ["sh", "-c", %q]
    volumes:
      - type: bind
        source: %s
        target: /input
        read_only: true
    deploy:
      mode: %s
      restart_policy: {condition: none}
%s
networks:
  default:
%s
`, command, input, mode, side, network)
					require.NoError(t, os.WriteFile(f.path, []byte(manifest), 0o600))
					file, err := compose.NewFileLoader().Load(f.ctx, f.path)
					require.NoError(t, err)
					return *file
				}
				req := deployments.ExecuteRequest{Stack: f.request.Stack, Desired: load(), Commit: "broken", SourceChanged: true}
				started := time.Now()
				failed, err := f.executor.Execute(f.ctx, req)
				require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
				require.Less(t, time.Since(started), 30*time.Second, "a known failure must finish before the 40s verification deadline")
				require.Equal(t, model.Failed, failed.Status)
				require.Equal(t, "convergence_failed", failed.ErrorCode)
				first := f.inspect(t, "job")
				require.NotNil(t, first.JobStatus)
				stored := f.latest(t)
				require.False(t, stored.Retryable)
				require.Contains(t, stored.ErrorDiagnostic, "exit_code=23")
				assert.Contains(t, stored.ErrorDiagnostic, "state=failed")
				assertDeferredExecutions(t, f, req, stored.ID, 1)

				// An unchanged explicit retry must execute a fresh iteration, then report
				// that iteration's failure rather than reusing the previous task.
				req.ManualRetry = true
				retried, err := f.executor.Execute(f.ctx, req)
				require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
				require.Equal(t, "convergence_failed", retried.ErrorCode)
				second := f.inspect(t, "job")
				require.Greater(t, second.JobStatus.JobIteration.Index, first.JobStatus.JobIteration.Index)
				require.Equal(t, first.Spec.TaskTemplate.ForceUpdate+1, second.Spec.TaskTemplate.ForceUpdate)
				retryDiagnostic := f.latest(t).ErrorDiagnostic
				var failedTask string
				for _, task := range f.tasks(t, "job") {
					if task.JobIteration != nil && *task.JobIteration == second.JobStatus.JobIteration && task.CurrentState == swarm.TaskStateFailed {
						failedTask = task.ID
					}
				}
				require.NotEmpty(t, failedTask)
				require.Contains(t, retryDiagnostic, "id="+failedTask)

				// Fix either an external input (identical Compose/spec) or the job command.
				if recovery == "unchanged-spec-input" {
					require.NoError(t, os.WriteFile(exitFile, []byte("0"), 0o600))
				} else {
					command = "exit 0"
					req.Commit = "fixed"
				}
				req.Desired = load()
				recovered, err := f.executor.Execute(f.ctx, req)
				require.NoError(t, err)
				require.Equal(t, model.Succeeded, recovered.Status)
				completed := f.inspect(t, "job")
				require.Greater(t, completed.JobStatus.JobIteration.Index, second.JobStatus.JobIteration.Index)
				expectedForce := second.Spec.TaskTemplate.ForceUpdate
				if recovery == "unchanged-spec-input" {
					expectedForce++
				}
				require.Equal(t, expectedForce, completed.Spec.TaskTemplate.ForceUpdate)
				var completedTask string
				for _, task := range f.tasks(t, "job") {
					if task.JobIteration != nil && *task.JobIteration == completed.JobStatus.JobIteration && task.CurrentState == swarm.TaskStateComplete {
						completedTask = task.ID
					}
				}
				require.NotEmpty(t, completedTask, "the fresh iteration actually completed")
				_ = f.settledJob(t)
				settled := taskIDs(f.tasks(t, "job"))
				req.ManualRetry = false
				for range 2 {
					noop, executeErr := f.executor.Execute(f.ctx, req)
					require.NoError(t, executeErr)
					require.Nil(t, noop.Attempt, "no-op sync must not execute a completed job")
				}
				// Updating a different service must not restart the completed job, even
				// while Docker retains failed tasks from earlier iterations.
				side = "  other:\n    image: busybox:1.36\n    command: [sleep, '600']\n"
				req.Desired, req.Commit = load(), "other-service"
				updated, err := f.executor.Execute(f.ctx, req)
				require.NoError(t, err)
				require.Equal(t, model.Succeeded, updated.Status)
				after := f.inspect(t, "job")
				assert.Equal(t, completed.JobStatus.JobIteration, after.JobStatus.JobIteration)
				assert.Equal(t, completed.Spec.TaskTemplate.ForceUpdate, after.Spec.TaskTemplate.ForceUpdate)
				_ = f.settledJob(t)
				assert.Equal(t, settled, taskIDs(f.tasks(t, "job")), "completed job task history must not grow")
			})
		}
	}
}

func TestRealSwarmJobManualRetryAfterMaxAttempts(t *testing.T) {
	for _, tc := range []struct{ name, condition string }{
		{"on-failure", "condition: on-failure, "},
		{"any", "condition: any, "},
		{"default", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLifecycleFixture(t, true)
			input := t.TempDir()
			exitFile := filepath.Join(input, "exit-code")
			require.NoError(t, os.WriteFile(exitFile, []byte("23"), 0o600))
			services := fmt.Sprintf(`  job:
    image: busybox:1.36
    command: ["sh", "-c", "exit $(cat /input/exit-code)"]
    volumes:
      - type: bind
        source: %s
        target: /input
        read_only: true
    deploy:
      mode: replicated-job
      restart_policy: {%smax_attempts: 1, delay: 100ms}
`, input, tc.condition)
			req := deployments.ExecuteRequest{
				Stack: f.request.Stack, Desired: f.manifest(t, services), Commit: "failed", SourceChanged: true,
			}
			failed, err := f.executor.Execute(f.ctx, req)
			require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
			require.Equal(t, model.Failed, failed.Status)
			first := f.inspect(t, "job")
			require.NotNil(t, first.JobStatus)
			var failedIDs []string
			for _, task := range f.tasks(t, "job") {
				if task.JobIteration != nil && *task.JobIteration == first.JobStatus.JobIteration &&
					task.CurrentState == swarm.TaskStateFailed {
					failedIDs = append(failedIDs, task.ID)
				}
			}
			require.Len(t, failedIDs, 2, "Docker must exhaust the initial execution and one automatic retry")

			require.NoError(t, os.WriteFile(exitFile, []byte("0"), 0o600))
			req.ManualRetry = true
			retried, err := f.executor.Execute(f.ctx, req)
			require.NoError(t, err)
			require.Equal(t, model.Succeeded, retried.Status)
			second := f.inspect(t, "job")
			require.Greater(t, second.JobStatus.JobIteration.Index, first.JobStatus.JobIteration.Index)
			require.Equal(t, first.Spec.TaskTemplate.ForceUpdate+1, second.Spec.TaskTemplate.ForceUpdate)
			var completedID string
			for _, task := range f.tasks(t, "job") {
				if task.JobIteration != nil && *task.JobIteration == second.JobStatus.JobIteration &&
					task.CurrentState == swarm.TaskStateComplete {
					completedID = task.ID
				}
			}
			require.NotEmpty(t, completedID, "Docker must schedule a new successful job task")
			require.NotContains(t, failedIDs, completedID)

			_, err = f.executor.Execute(f.ctx, req)
			require.NoError(t, err)
			completed := f.inspect(t, "job")
			assert.Equal(t, second.JobStatus.JobIteration, completed.JobStatus.JobIteration)
			assert.Equal(t, second.Spec.TaskTemplate.ForceUpdate, completed.Spec.TaskTemplate.ForceUpdate)
		})
	}
}
