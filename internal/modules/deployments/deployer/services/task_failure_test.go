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

func TestNonRestartingTaskFailure(t *testing.T) {
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
			actual, found := NonRestartingTaskFailure(spec, tc.tasks, tc.since)
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
	initial := swarm.ServiceTask{ID: "first", Slot: 1, Node: "node-a", NodeInfo: &swarm.Node{Availability: "active"},
		DesiredState: swarm.TaskStateShutdown, CurrentState: swarm.TaskStateFailed,
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
	old.CreatedAt = now.Add(-2 * time.Minute)
	old.UpdatedAt = old.CreatedAt
	oldRetry := retryOne
	oldRetry.CreatedAt = now.Add(-time.Minute)
	oldRetry.UpdatedAt = oldRetry.CreatedAt.Add(time.Second)

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
			max: &maxAttempts, window: &window, tasks: []swarm.ServiceTask{old, oldRetry, retryTwo}},
		{name: "global healthy node does not override failure", global: true,
			condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{initial,
				{ID: "healthy", Node: "node-b", DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning,
					CreatedAt: now, UpdatedAt: now}}, want: true},
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
					NodeInfo: &swarm.Node{Availability: "active"}, DesiredState: swarm.TaskStateShutdown,
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

func TestApplyRetryAfterCompletedRollout(t *testing.T) {
	for _, tc := range []struct {
		name        string
		global      bool
		drained     bool
		ineligible  bool
		finite      bool
		healthy     bool
		replacement swarm.TaskState
		disabled    bool
		external    bool
		wantUpdate  bool
	}{
		{name: "replicated terminal failure", wantUpdate: true},
		{name: "global terminal failure", global: true, wantUpdate: true},
		{name: "replicated finite attempts exhausted", finite: true, wantUpdate: true},
		{name: "global finite attempts exhausted", global: true, finite: true, wantUpdate: true},
		{name: "drained global failure", global: true, drained: true},
		{name: "drained global exhausted attempts", global: true, drained: true, finite: true},
		{name: "global node no longer matches placement", global: true, ineligible: true},
		{name: "exhausted global node no longer matches placement", global: true, ineligible: true, finite: true},
		{name: "healthy service", healthy: true},
		{name: "healthy global service", global: true, healthy: true},
		{name: "replicated replacement pending", replacement: swarm.TaskStatePending},
		{name: "global replacement pending", global: true, replacement: swarm.TaskStatePending},
		{name: "replicated replacement running", replacement: swarm.TaskStateRunning},
		{name: "global replacement running", global: true, replacement: swarm.TaskStateRunning},
		{name: "normal reconciliation", disabled: true},
		{name: "external replica management", external: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "busybox:1.36")
			desired.TaskTemplate.ForceUpdate = 4
			desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			if tc.global {
				desired.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
			}
			if tc.ineligible {
				desired.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.labels.zone == east"}}
			}
			if tc.finite {
				one := uint64(1)
				desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
					Condition: dockerswarm.RestartPolicyConditionOnFailure, MaxAttempts: &one,
				}
			}
			if tc.external {
				desired.Labels["org.swarm-deploy.service.sync.policy.replicasManaged"] = "false"
			}
			now := time.Now()
			current := service("api-id", 7, desired)
			current.CreatedAt = now.Add(-time.Minute)
			current.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStateCompleted}
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			if !tc.disabled && !tc.external {
				exit := 23
				task := swarm.ServiceTask{ID: "failed", Slot: 1, Node: "node-a", ExitCode: &exit,
					DesiredState: swarm.TaskStateShutdown,
					NodeInfo:     &swarm.Node{ID: "node-a", Availability: "active", Labels: map[string]string{"zone": "east"}},
					CurrentState: swarm.TaskStateFailed, CreatedAt: now, UpdatedAt: now,
					Spec: swarm.NewTaskSpec(desired.TaskTemplate)}
				if tc.healthy {
					task.CurrentState = swarm.TaskStateRunning
					task.DesiredState = swarm.TaskStateRunning
				}
				if tc.ineligible {
					task.NodeInfo.Labels["zone"] = "west"
				}
				tasks := []swarm.ServiceTask{task}
				if tc.finite || tc.replacement != "" {
					next := task
					next.ID, next.CreatedAt, next.UpdatedAt = "replacement", now.Add(time.Second), now.Add(time.Second)
					if tc.replacement != "" {
						next.CurrentState = tc.replacement
						next.DesiredState = swarm.TaskStateRunning
					}
					tasks = append(tasks, next)
				}
				if tc.drained || tc.ineligible {
					if tc.drained {
						for i := range tasks {
							tasks[i].DesiredState = swarm.TaskStateShutdown
							tasks[i].NodeInfo = &swarm.Node{ID: "node-a", Availability: "drain"}
						}
					}
					healthy := task
					healthy.ID, healthy.Node, healthy.CurrentState = "healthy", "node-b", swarm.TaskStateRunning
					healthy.DesiredState = swarm.TaskStateRunning
					healthy.NodeInfo = &swarm.Node{ID: "node-b", Availability: "active", Labels: map[string]string{"zone": "east"}}
					tasks = append(tasks, healthy)
				}
				calls := 1
				if tc.wantUpdate {
					calls++
				}
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return(tasks, nil).Times(calls)
			}
			if tc.wantUpdate {
				forced := cloneSpec(desired)
				forced.TaskTemplate.ForceUpdate++
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, forced, gomock.Any()).Return(nil)
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo",
				[]dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: !tc.disabled})
			require.NoError(t, err)
			assert.Equal(t, tc.wantUpdate, len(result.ChangedServices) == 1)
		})
	}
}

func TestTerminalServiceTaskFailureDesiredNodes(t *testing.T) {
	now := time.Now()
	maxAttempts := uint64(2)
	window := time.Second
	initial := swarm.ServiceTask{ID: "initial", Slot: 1, Node: "node-a",
		DesiredState: swarm.TaskStateShutdown, CurrentState: swarm.TaskStateFailed,
		CreatedAt: now, UpdatedAt: now.Add(time.Second)}
	retry := initial
	retry.ID, retry.CreatedAt, retry.UpdatedAt = "retry", now.Add(2*time.Second), now.Add(3*time.Second)
	active := initial
	active.ID, active.CreatedAt, active.UpdatedAt = "active", now.Add(4*time.Second), now.Add(5*time.Second)
	active.DesiredState = swarm.TaskStateRunning
	shutdownActive := active
	shutdownActive.DesiredState = swarm.TaskStateShutdown
	shutdownActive.NodeInfo = &swarm.Node{Availability: "active"}
	paused := shutdownActive
	paused.NodeInfo = &swarm.Node{Availability: "pause"}
	drained := shutdownActive
	drained.NodeInfo = &swarm.Node{Availability: "drain"}
	rejected := active
	rejected.CurrentState = swarm.TaskStateRejected
	removed := initial
	removed.DesiredState = swarm.TaskStateRemove
	healthy := active
	healthy.ID, healthy.Node, healthy.Slot, healthy.CurrentState = "healthy", "node-b", 2, swarm.TaskStateRunning
	pending := active
	pending.ID, pending.CreatedAt, pending.CurrentState = "pending", now.Add(6*time.Second), swarm.TaskStatePending

	for _, tc := range []struct {
		name       string
		replicated bool
		max        *uint64
		window     *time.Duration
		tasks      []swarm.ServiceTask
		wantID     string
	}{
		{name: "drained node", tasks: []swarm.ServiceTask{drained, healthy}},
		{name: "removed node", tasks: []swarm.ServiceTask{removed, healthy}},
		{name: "no desired nodes", tasks: []swarm.ServiceTask{initial}},
		{name: "active failure with healthy peer", tasks: []swarm.ServiceTask{active, healthy}, wantID: "active"},
		{name: "shutdown failure on active node", tasks: []swarm.ServiceTask{shutdownActive, healthy}, wantID: "active"},
		{name: "shutdown failure on paused node", tasks: []swarm.ServiceTask{paused, healthy}, wantID: "active"},
		{name: "active rejection with healthy peer", tasks: []swarm.ServiceTask{rejected, healthy}, wantID: "active"},
		{name: "active exhausted history", max: &maxAttempts, tasks: []swarm.ServiceTask{retry, active, initial, healthy}, wantID: "active"},
		{name: "shutdown exhausted history on active node", max: &maxAttempts, tasks: []swarm.ServiceTask{retry, shutdownActive, initial, healthy}, wantID: "active"},
		{name: "initial attempt is not a restart", max: &maxAttempts, tasks: []swarm.ServiceTask{initial, active, healthy}},
		{name: "attempts outside window", max: &maxAttempts, window: &window, tasks: []swarm.ServiceTask{initial, retry, active, healthy}},
		{name: "drained exhausted history", max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retry, drained, healthy}},
		{name: "pending replacement", max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retry, active, pending, healthy}},
		{name: "replicated shutdown failure", replicated: true, tasks: []swarm.ServiceTask{initial, healthy}, wantID: "initial"},
		{name: "replicated removed failure", replicated: true, tasks: []swarm.ServiceTask{removed, healthy}, wantID: "initial"},
		{name: "replicated exhausted history", replicated: true, max: &maxAttempts, tasks: []swarm.ServiceTask{initial, retry, drained, healthy}, wantID: "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := serviceSpec("api", "busybox:1.36")
			if !tc.replicated {
				spec.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
			}
			spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
				Condition: dockerswarm.RestartPolicyConditionNone, MaxAttempts: tc.max, Window: tc.window,
			}
			if tc.max != nil {
				spec.TaskTemplate.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionOnFailure
			}
			tasks := append([]swarm.ServiceTask(nil), tc.tasks...)
			for i := range tasks {
				tasks[i].Spec = swarm.NewTaskSpec(spec.TaskTemplate)
			}
			failed, found := TerminalServiceTaskFailure(spec, tasks, now.Add(-time.Minute))
			assert.Equal(t, tc.wantID != "", found)
			assert.Equal(t, tc.wantID, failed.ID)

			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			current := service("api-id", 7, spec)
			current.CreatedAt = now.Add(-time.Minute)
			manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return(tasks, nil)
			required, err := New(manager, &registry.NopAuthManager{}).retryRequired(t.Context(), current, true)
			require.NoError(t, err)
			assert.Equal(t, tc.wantID != "", required)
		})
	}
}

func TestRetryRequiredPreservesUpdateStateHandling(t *testing.T) {
	for _, tc := range []struct {
		state dockerswarm.UpdateState
		want  bool
	}{
		{state: dockerswarm.UpdateStateUpdating},
		{state: dockerswarm.UpdateStatePaused, want: true},
		{state: dockerswarm.UpdateStateRollbackStarted, want: true},
		{state: dockerswarm.UpdateStateRollbackPaused, want: true},
		{state: dockerswarm.UpdateStateRollbackCompleted, want: true},
		{state: dockerswarm.UpdateState("failed"), want: true},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			spec := serviceSpec("api", "busybox:1.36")
			spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			current := service("api-id", 7, spec)
			current.UpdateStatus = &dockerswarm.UpdateStatus{State: tc.state}
			got, err := New(manager, &registry.NopAuthManager{}).retryRequired(t.Context(), current, true)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			// Existing rollout handling must not inspect terminal task history.
		})
	}
}
