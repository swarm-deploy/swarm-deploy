package services

import (
	"testing"

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
			assert.Equal(t, tc.update, len(changed) > 0)
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
	assert.Equal(t, []string{"job"}, changed)
	assert.Zero(t, *desired.Mode.Replicated.Replicas)
	assert.Zero(t, desired.TaskTemplate.ForceUpdate)
}

func TestExternalTaskRecreatingUpdateSafety(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replicas uint64
		task     swarm.TaskState
		retry    bool
		blocked  bool
	}{
		{name: "running job", replicas: 1, task: swarm.TaskStateRunning, blocked: true},
		{name: "completed job with nonzero replicas", replicas: 1, task: swarm.TaskStateComplete, blocked: true},
		{name: "zero replicas but task stopping", task: swarm.TaskStateRunning, blocked: true},
		{name: "zero replicas but task preparing", task: swarm.TaskStatePreparing, blocked: true},
		{name: "unknown task state", task: "future-state", blocked: true},
		{name: "idle with completed task", task: swarm.TaskStateComplete},
		{name: "idle without tasks"},
		{name: "retry cannot execute outside schedule", replicas: 1, retry: true, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := externalSpec("job")
			current := service("id", 1, cloneSpec(desired))
			*current.Spec.Mode.Replicated.Replicas = tc.replicas
			current.Spec.TaskTemplate.ForceUpdate = 10
			if tc.retry {
				current.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}
			} else {
				desired.TaskTemplate.ContainerSpec.Env = []string{"CHANGED=yes"}
			}
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(current, nil)
			if tc.replicas == 0 {
				var tasks []swarm.ServiceTask
				if tc.task != "" {
					tasks = []swarm.ServiceTask{{CurrentState: tc.task, DesiredState: swarm.TaskStateShutdown}}
				}
				manager.EXPECT().ListTasks(gomock.Any(), "id").Return(tasks, nil)
			}
			if !tc.blocked {
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
				assert.Empty(t, changed)
			} else {
				require.NoError(t, err)
				assert.Equal(t, []string{"job"}, changed)
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
	assert.Empty(t, changed)
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
	assert.Equal(t, []string{"web"}, changed)
}
