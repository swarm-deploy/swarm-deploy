package services

import (
	"errors"
	"fmt"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func externalSpec(name string) dockerswarm.ServiceSpec {
	spec := serviceSpec(name, "alpine:3")
	spec.Labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = "false"
	replicas := uint64(0)
	spec.Mode.Replicated.Replicas = &replicas
	return spec
}

func TestReplicaOwnershipDuringApply(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		update       bool
	}{
		{name: "external", policy: "false"},
		{name: "external boolean alias", policy: " OFF "},
		{name: "default", update: true},
		{name: "explicit managed", policy: "true", update: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := externalSpec("job")
			delete(desired.Labels, labelsdict.ServiceSyncPolicyReplicasManagedLabelKey)
			if tc.policy != "" {
				desired.Labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = tc.policy
			}
			current := cloneSpec(desired)
			*current.Mode.Replicated.Replicas = 1
			current.TaskTemplate.ForceUpdate = 42
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(service("job-id", 42, current), nil)
			if tc.update {
				manager.EXPECT().Update(gomock.Any(), "job-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
						assert.Zero(t, *spec.Mode.Replicated.Replicas)
						assert.Equal(t, uint64(42), spec.TaskTemplate.ForceUpdate)
						return nil
					})
			}
			changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
			require.NoError(t, err)
			assert.Equal(t, tc.update, len(changed.ChangedServices) > 0)
			assert.Zero(t, *desired.Mode.Replicated.Replicas, "caller spec is immutable")
		})
	}
}

func TestMetadataUpdateRebasesExternalValuesOnConflict(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := externalSpec("job")
	current := cloneSpec(desired)
	*current.Mode.Replicated.Replicas = 1
	current.TaskTemplate.ForceUpdate = 7
	desired.Labels["description"] = "updated"
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(service("id", 1, current), nil)
	for _, step := range []struct{ version, replicas, force uint64 }{{1, 1, 7}, {2, 2, 8}} {
		manager.EXPECT().Update(gomock.Any(), "id", dockerswarm.Version{Index: step.version}, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
				assert.Equal(t, step.replicas, *spec.Mode.Replicated.Replicas)
				assert.Equal(t, step.force, spec.TaskTemplate.ForceUpdate)
				assert.Equal(t, "updated", spec.Labels["description"])
				if step.version == 1 {
					return swarm.ErrServiceConflict
				}
				return nil
			})
		if step.version == 1 {
			newer := cloneSpec(current)
			*newer.Mode.Replicated.Replicas = 2
			newer.TaskTemplate.ForceUpdate = 8
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(service("id", 2, newer), nil)
		}
	}
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"job"}, changed.ChangedServices)
	assert.Equal(t, uint64(8), changed.ExternalUpdates["job"].ForceUpdate, "receipt must use the generation from the successful optimistic retry")
	assert.Zero(t, *desired.Mode.Replicated.Replicas)
	assert.Zero(t, desired.TaskTemplate.ForceUpdate)
}

func TestExternalTaskRecreatingUpdateSafety(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replicas  uint64
		task      swarm.TaskState
		restart   bool
		stale     bool
		removed   bool
		retry     bool
		unchanged bool
		blocked   bool
		deferred  bool
	}{
		{name: "running job", replicas: 1, task: swarm.TaskStateRunning, blocked: true},
		{name: "completed long-running service with nonzero replicas", replicas: 1, task: swarm.TaskStateComplete, blocked: true},
		{name: "zero replicas but task stopping", task: swarm.TaskStateRunning, blocked: true, deferred: true},
		{name: "zero replicas but task preparing", task: swarm.TaskStatePreparing, blocked: true, deferred: true},
		{name: "unknown task state", task: "future-state", blocked: true, deferred: true},
		{name: "idle with completed task", task: swarm.TaskStateComplete},
		{name: "idle without tasks"},
		{name: "completed restart-none job is parked", replicas: 1, task: swarm.TaskStateComplete, restart: true},
		{name: "failed restart-none job is parked", replicas: 1, task: swarm.TaskStateFailed, restart: true},
		{name: "restart-none job without a task in its slot is deferred", replicas: 1, restart: true, blocked: true, deferred: true},
		{name: "scheduled run accepted before its task exists is deferred", replicas: 1, task: swarm.TaskStateComplete, restart: true, stale: true, blocked: true, deferred: true},
		{name: "restart-none job with removed slot task is deferred", replicas: 1, task: swarm.TaskStateComplete, restart: true, removed: true, blocked: true, deferred: true},
		{name: "running restart-none job is deferred", replicas: 1, task: swarm.TaskStateRunning, restart: true, blocked: true, deferred: true},
		{name: "retry does not execute unchanged job outside schedule", replicas: 1, retry: true, unchanged: true},
		{name: "retry does not park unchanged restart-none job", replicas: 1, restart: true, retry: true, unchanged: true},
		{name: "retry blocks unsafe changed template", replicas: 1, task: swarm.TaskStateRunning, retry: true, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := externalSpec("job")
			if tc.restart {
				desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			}
			current := service("id", 1, cloneSpec(desired))
			*current.Spec.Mode.Replicated.Replicas = tc.replicas
			current.Spec.TaskTemplate.ForceUpdate = 10
			if tc.retry {
				current.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}
			}
			if !tc.unchanged {
				desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
			}
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(current, nil)
			if (tc.replicas == 0 || tc.restart) && !tc.unchanged {
				var tasks []swarm.ServiceTask
				if tc.task != "" {
					task := swarm.ServiceTask{
						Slot: 1, CurrentState: tc.task, DesiredState: swarm.TaskStateShutdown, Spec: current.Spec.TaskTemplate,
					}
					if tc.stale {
						task.Spec.ForceUpdate-- // Created before the scheduler's latest ForceUpdate.
					}
					if tc.removed {
						task.DesiredState = swarm.TaskStateRemove
					}
					tasks = []swarm.ServiceTask{task}
				}
				manager.EXPECT().ListTasks(gomock.Any(), "id").Return(tasks, nil)
			}
			if !tc.blocked && !tc.unchanged {
				manager.EXPECT().Update(gomock.Any(), "id", current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
						assert.Zero(t, *spec.Mode.Replicated.Replicas)
						assert.Equal(t, uint64(10), spec.TaskTemplate.ForceUpdate)
						return nil
					})
			}
			changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: tc.retry})
			if tc.blocked {
				require.ErrorIs(t, err, ErrUpdateBlocked)
				assert.ErrorContains(t, err, "service job")
				assert.Equal(t, tc.deferred, errors.Is(err, ErrUpdateDeferred))
				assert.Empty(t, changed.ChangedServices)
			} else {
				require.NoError(t, err)
				if tc.unchanged {
					assert.Empty(t, changed.ChangedServices)
				} else {
					assert.Equal(t, []string{"job"}, changed.ChangedServices)
				}
			}
		})
	}
}

func TestExternalJobStartsDuringOptimisticRetry(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := externalSpec("job")
	current := service("id", 1, cloneSpec(desired))
	desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(current, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "id").Return(nil, nil)
	manager.EXPECT().Update(gomock.Any(), "id", current.Version, gomock.Any(), gomock.Any()).Return(swarm.ErrServiceConflict)
	newer := service("id", 2, cloneSpec(current.Spec))
	*newer.Spec.Mode.Replicated.Replicas = 1
	newer.Spec.TaskTemplate.ForceUpdate = 11
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(newer, nil)
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.ErrorIs(t, err, ErrUpdateBlocked)
	assert.Empty(t, changed.ChangedServices)
}

func TestParkedExternalJobStartsDuringOptimisticRetry(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := externalSpec("job")
	desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
	current := service("id", 1, cloneSpec(desired))
	*current.Spec.Mode.Replicated.Replicas = 1
	current.Spec.TaskTemplate.ForceUpdate = 10
	desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(current, nil)
	completed := []swarm.ServiceTask{{Slot: 1, CurrentState: swarm.TaskStateComplete,
		DesiredState: swarm.TaskStateShutdown, Spec: current.Spec.TaskTemplate}}
	manager.EXPECT().ListTasks(gomock.Any(), "id").Return(completed, nil)
	manager.EXPECT().Update(gomock.Any(), "id", current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
			assert.Zero(t, *spec.Mode.Replicated.Replicas, "idle job is parked with the template change")
			assert.Equal(t, uint64(10), spec.TaskTemplate.ForceUpdate)
			return swarm.ErrServiceConflict
		})
	// The scheduler started a run between inspection and update.
	newer := service("id", 2, cloneSpec(current.Spec))
	newer.Spec.TaskTemplate.ForceUpdate = 11
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(newer, nil)
	running := []swarm.ServiceTask{{CurrentState: swarm.TaskStateRunning, DesiredState: swarm.TaskStateRunning}}
	manager.EXPECT().ListTasks(gomock.Any(), "id").Return(running, nil)
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{})
	require.ErrorIs(t, err, ErrUpdateBlocked)
	require.ErrorIs(t, err, ErrUpdateDeferred)
	assert.Empty(t, changed.ChangedServices)
	assert.Zero(t, *desired.Mode.Replicated.Replicas, "caller spec is immutable")
}

func TestExecutionSettled(t *testing.T) {
	template := dockerswarm.TaskSpec{ForceUpdate: 5, ContainerSpec: &dockerswarm.ContainerSpec{Image: "alpine:3"}}
	older := template
	older.ForceUpdate = 4
	at := func(seconds int) time.Time { return time.Unix(int64(seconds), 0) }
	done := func(slot, created int, spec dockerswarm.TaskSpec) swarm.ServiceTask {
		return swarm.ServiceTask{Slot: slot, CreatedAt: at(created), CurrentState: swarm.TaskStateComplete,
			DesiredState: swarm.TaskStateShutdown, Spec: spec}
	}
	for _, tc := range []struct {
		name     string
		replicas uint64
		tasks    []swarm.ServiceTask
		settled  bool
	}{
		{name: "every slot ran the current template", replicas: 2,
			tasks: []swarm.ServiceTask{done(1, 1, template), done(2, 1, template)}, settled: true},
		{name: "scaled up slot has no task yet", replicas: 2, tasks: []swarm.ServiceTask{done(1, 1, template)}},
		{name: "newest slot task predates ForceUpdate", replicas: 1,
			tasks: []swarm.ServiceTask{done(1, 1, template), done(1, 2, older)}},
		{name: "history before the current run is ignored", replicas: 1,
			tasks: []swarm.ServiceTask{done(1, 1, older), done(1, 2, template)}, settled: true},
		{name: "tasks outside desired slots are ignored", replicas: 1,
			tasks: []swarm.ServiceTask{done(1, 1, template), done(2, 2, older)}, settled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.settled, executionSettled(template, tc.replicas, tc.tasks))
		})
	}
}

func TestDeferredOnlyWhenEveryBlockIsTransient(t *testing.T) {
	busy := fmt.Errorf("%w: service a still has active tasks", errUpdateBusy)
	permanent := fmt.Errorf("%w: service b requires zero replicas", ErrUpdateBlocked)
	for _, tc := range []struct {
		name     string
		blocked  []error
		deferred bool
	}{
		{name: "nothing blocked"},
		{name: "only active tasks", blocked: []error{busy}, deferred: true},
		{name: "operator action required", blocked: []error{permanent}},
		{name: "mixed", blocked: []error{busy, permanent}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := blockedApplyError(tc.blocked)
			if len(tc.blocked) == 0 {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrUpdateBlocked)
			assert.Equal(t, tc.deferred, errors.Is(err, ErrUpdateDeferred))
		})
	}
}

func TestBlockedExternalJobDoesNotPreventIndependentApply(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	desired := externalSpec("job")
	current := cloneSpec(desired)
	*current.Mode.Replicated.Replicas = 1
	desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
	manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(service("id", 1, current), nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_web").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("web-id", nil)
	changed, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired, serviceSpec("web", "nginx:1")}, ApplyOptions{})
	require.ErrorIs(t, err, ErrUpdateBlocked)
	assert.Equal(t, []string{"web"}, changed.ChangedServices)
}

func TestExternalReplicaOwnershipRejectsUnsupportedModeBeforeDockerAccess(t *testing.T) {
	for _, mode := range []dockerswarm.ServiceMode{
		{Global: &dockerswarm.GlobalService{}},
		{ReplicatedJob: &dockerswarm.ReplicatedJob{}},
		{GlobalJob: &dockerswarm.GlobalJob{}},
	} {
		t.Run(fmt.Sprintf("%#v", mode), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			spec := externalSpec("job")
			spec.Mode = mode
			assert.False(t, SpecsEqual(spec, spec))
			_, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{spec}, ApplyOptions{})
			require.ErrorContains(t, err, "only for replicated services")
		})
	}
}

func TestRetryUnchangedExternalJobAppliesIndependentFailedService(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	job := externalSpec("backup")
	current := cloneSpec(job)
	*current.Mode.Replicated.Replicas = 1
	current.TaskTemplate.ForceUpdate = 42
	backup := service("backup-id", 1, current)
	backup.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}
	manager.EXPECT().Inspect(gomock.Any(), "demo_backup").Return(backup, nil)
	web := serviceSpec("web", "nginx:1")
	failed := service("web-id", 2, cloneSpec(web))
	failed.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}
	manager.EXPECT().Inspect(gomock.Any(), "demo_web").Return(failed, nil)
	manager.EXPECT().Update(gomock.Any(), "web-id", failed.Version, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, _ string, _ dockerswarm.Version, applied dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
			assert.Equal(t, uint64(1), applied.TaskTemplate.ForceUpdate)
			return nil
		})
	result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{web, job}, ApplyOptions{RetryFailedUpdates: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, result.ChangedServices)
	assert.Empty(t, result.ExternalUpdates)
	assert.Equal(t, uint64(42), backup.Spec.TaskTemplate.ForceUpdate)
}
