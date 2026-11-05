package services

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestTerminalServiceTaskFailure(t *testing.T) {
	now := time.Now()
	exit, zero := 23, 0
	failed := swarm.ServiceTask{ID: "failed", Slot: 1, CurrentState: swarm.TaskStateFailed,
		CreatedAt: now, UpdatedAt: now, ExitCode: &exit}
	withoutExit := failed
	withoutExit.ExitCode = nil
	zeroExit := failed
	zeroExit.ExitCode = &zero
	rejected := withoutExit
	rejected.CurrentState = swarm.TaskStateRejected
	rejectedZeroExit := rejected
	rejectedZeroExit.ExitCode = &zero
	rejectedRemovedSlot := rejected
	rejectedRemovedSlot.Slot = 2
	replacement := swarm.ServiceTask{ID: "replacement", Slot: 1, CurrentState: swarm.TaskStatePending,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	runningReplacement := replacement
	runningReplacement.CurrentState = swarm.TaskStateRunning
	removedSlot := withoutExit
	removedSlot.Slot = 2
	for _, tc := range []struct {
		name      string
		condition dockerswarm.RestartPolicyCondition
		tasks     []swarm.ServiceTask
		since     time.Time
		replicas  uint64
		want      bool
	}{
		{name: "terminal current task without exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{withoutExit}, replicas: 1, want: true},
		{name: "known zero exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{zeroExit}, replicas: 1},
		{name: "terminal current task", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}, replicas: 1, want: true},
		{name: "automatic replacement allowed", condition: dockerswarm.RestartPolicyConditionAny, tasks: []swarm.ServiceTask{failed}, replicas: 1},
		{name: "failure restart allowed", condition: dockerswarm.RestartPolicyConditionOnFailure, tasks: []swarm.ServiceTask{failed}, replicas: 1},
		{name: "new generation pending", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}, since: now.Add(time.Second), replicas: 1},
		{name: "replacement supersedes failure", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{replacement, failed}, replicas: 1},
		{name: "old failure without exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{withoutExit}, since: now.Add(time.Second), replicas: 1},
		{name: "pending replacement supersedes failure without exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{replacement, withoutExit}, replicas: 1},
		{name: "running replacement supersedes failure without exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{withoutExit, runningReplacement}, replicas: 1},
		{name: "removed slot without exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{removedSlot}, replicas: 1},
		{name: "scaled down slot ignored", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}},
		{name: "rejected current task", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejected}, replicas: 1, want: true},
		{name: "rejected task with zero exit code", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejectedZeroExit}, replicas: 1, want: true},
		{name: "rejected task with automatic restart", condition: dockerswarm.RestartPolicyConditionAny, tasks: []swarm.ServiceTask{rejected}, replicas: 1},
		{name: "rejected task with failure restart", condition: dockerswarm.RestartPolicyConditionOnFailure, tasks: []swarm.ServiceTask{rejected}, replicas: 1},
		{name: "rejection before generation", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejected}, since: now.Add(time.Second), replicas: 1},
		{name: "pending replacement supersedes rejection", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{replacement, rejected}, replicas: 1},
		{name: "running replacement supersedes rejection", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejected, runningReplacement}, replicas: 1},
		{name: "rejected task outside desired slots", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejectedRemovedSlot}, replicas: 1},
		{name: "rejected task after scale to zero", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{rejected}},
		{name: "no tasks yet", condition: dockerswarm.RestartPolicyConditionNone, replicas: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := serviceSpec("api", "busybox:1.36")
			spec.Mode.Replicated.Replicas = &tc.replicas
			spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: tc.condition}
			for i := range tc.tasks {
				tc.tasks[i].Spec = swarm.NewTaskSpec(spec.TaskTemplate)
			}
			actual, found := TerminalServiceTaskFailure(spec, tc.tasks, tc.since)
			assert.Equal(t, tc.want, found)
			if found {
				assert.Equal(t, failed.ID, actual.ID)
			}
		})
	}
}

func TestApplyExplicitRetryOfInitialFailure(t *testing.T) {
	exit := 23
	for _, tc := range []struct {
		name       string
		retry      bool
		exitCode   *int
		taskOffset time.Duration
		state      swarm.TaskState
	}{
		{name: "unchanged failure is not forced", exitCode: &exit},
		{name: "explicit retry forces new generation", retry: true, exitCode: &exit, taskOffset: time.Second},
		{name: "unchanged failure without exit code is not forced"},
		{name: "explicit retry without exit code forces new generation", retry: true, taskOffset: time.Second},
		{name: "allocation metadata updated after initial task creation", retry: true, exitCode: &exit, taskOffset: -time.Millisecond},
		{name: "allocation metadata updated after initial task without exit code", retry: true, taskOffset: -time.Millisecond},
		{name: "unchanged rejection is not forced", state: swarm.TaskStateRejected},
		{name: "explicit retry of rejection forces new generation", state: swarm.TaskStateRejected, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "busybox:1.36")
			desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			current := service("api-id", 7, desired)
			current.CreatedAt = time.Now().Add(-time.Second)
			current.UpdatedAt = current.CreatedAt.Add(time.Second)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			if tc.retry {
				state := tc.state
				if state == "" {
					state = swarm.TaskStateFailed
				}
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{{
					ID: "initial-terminal", Slot: 1, CurrentState: state, ExitCode: tc.exitCode,
					CreatedAt: current.UpdatedAt.Add(tc.taskOffset), Spec: swarm.NewTaskSpec(desired.TaskTemplate),
				}}, nil).Times(2)
				forced := cloneSpec(desired)
				forced.TaskTemplate.ForceUpdate++
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, forced, gomock.Any()).Return(nil)
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: tc.retry})
			require.NoError(t, err)
			if tc.retry {
				assert.Equal(t, []string{"api"}, result.ChangedServices)
			} else {
				assert.Empty(t, result.ChangedServices)
			}
		})
	}
}

func TestTerminalServiceTaskFailureWithFiniteRetries(t *testing.T) {
	now := time.Now()
	exit := 1
	maxAttempts := uint64(2)
	window := 30 * time.Second
	initial := swarm.ServiceTask{ID: "first", Slot: 1, Node: "node-a", CurrentState: swarm.TaskStateFailed,
		CreatedAt: now, UpdatedAt: now.Add(time.Second), ExitCode: &exit}
	retryOne := initial
	retryOne.ID = "retry-1"
	retryOne.CreatedAt = now.Add(2 * time.Second)
	retryOne.UpdatedAt = now.Add(3 * time.Second)
	retryTwo := initial
	retryTwo.ID = "retry-2"
	retryTwo.CreatedAt = now.Add(4 * time.Second)
	retryTwo.UpdatedAt = now.Add(5 * time.Second)
	pending := retryTwo
	pending.ID = "replacement"
	pending.CreatedAt = now.Add(6 * time.Second)
	pending.UpdatedAt = pending.CreatedAt
	pending.CurrentState = swarm.TaskStatePending
	old := retryOne
	old.ID = "old-generation"
	old.CreatedAt = now.Add(-time.Minute)
	old.UpdatedAt = old.CreatedAt

	for _, tc := range []struct {
		name      string
		global    bool
		condition dockerswarm.RestartPolicyCondition
		max       *uint64
		window    *time.Duration
		tasks     []swarm.ServiceTask
		want      bool
	}{
		{name: "replicated retries exhausted", condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retryOne, retryTwo}, want: true},
		{name: "global retries exhausted", global: true, condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retryOne, retryTwo}, want: true},
		{name: "global no restart", global: true, condition: dockerswarm.RestartPolicyConditionNone,
			tasks: []swarm.ServiceTask{initial}, want: true},
		{name: "replicated attempts remaining", condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retryOne}},
		{name: "global attempts remaining", global: true, condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retryOne}},
		{name: "replacement pending", condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retryOne, retryTwo, pending}},
		{name: "unlimited restarts", condition: dockerswarm.RestartPolicyConditionOnFailure,
			tasks: []swarm.ServiceTask{initial, retryOne, retryTwo}},
		{name: "window limits counted attempts", condition: dockerswarm.RestartPolicyConditionOnFailure,
			max: &maxAttempts, window: &window, tasks: []swarm.ServiceTask{old, retryOne, retryTwo}},
		{name: "global healthy node does not override failure", global: true,
			condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{initial,
				{ID: "healthy", Node: "node-b", CurrentState: swarm.TaskStateRunning,
					CreatedAt: now, UpdatedAt: now}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := serviceSpec("api", "busybox:1.36")
			if tc.global {
				spec.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
			}
			spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
				Condition: tc.condition, MaxAttempts: tc.max, Window: tc.window,
			}
			tasks := append([]swarm.ServiceTask(nil), tc.tasks...)
			for i := range tasks {
				tasks[i].Spec = swarm.NewTaskSpec(spec.TaskTemplate)
			}
			_, found := TerminalServiceTaskFailure(spec, tasks, now.Add(-time.Hour))
			assert.Equal(t, tc.want, found)
		})
	}
}

func TestApplyManualRetryRestartsExhaustedServices(t *testing.T) {
	now := time.Now()
	exit := 1
	maxAttempts := uint64(1)
	for _, tc := range []struct {
		name   string
		global bool
		max    *uint64
		retry  bool
	}{
		{name: "global no-restart retry", global: true, retry: true},
		{name: "replicated exhausted retry", max: &maxAttempts, retry: true},
		{name: "no explicit retry", max: &maxAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "busybox:1.36")
			if tc.global {
				desired.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
				desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
					Condition: dockerswarm.RestartPolicyConditionNone,
				}
			} else {
				desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
					Condition: dockerswarm.RestartPolicyConditionOnFailure, MaxAttempts: tc.max,
				}
			}
			current := service("api-id", 7, desired)
			current.CreatedAt = now.Add(-time.Minute)
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			if tc.retry {
				first := swarm.ServiceTask{ID: "initial", Slot: 1, Node: "node-a",
					CurrentState: swarm.TaskStateFailed, ExitCode: &exit,
					CreatedAt: now.Add(-3 * time.Second), UpdatedAt: now.Add(-2 * time.Second),
					Spec: swarm.NewTaskSpec(desired.TaskTemplate)}
				tasks := []swarm.ServiceTask{first}
				if !tc.global {
					again := first
					again.ID = "restart"
					again.CreatedAt = now.Add(-time.Second)
					again.UpdatedAt = now
					tasks = append(tasks, again)
				}
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return(tasks, nil).Times(2)
				forced := cloneSpec(desired)
				forced.TaskTemplate.ForceUpdate++
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, forced, gomock.Any()).Return(nil)
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(
				t.Context(), "demo", []dockerswarm.ServiceSpec{desired},
				ApplyOptions{RetryFailedUpdates: tc.retry},
			)
			require.NoError(t, err)
			assert.Equal(t, tc.retry, len(result.ChangedServices) == 1)
		})
	}
}
