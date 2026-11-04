package waiter

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestPlacementOnlyUpdateReusesCompatibleRunningTasks(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		t.Run(mode, func(t *testing.T) {
			controller := gomock.NewController(t)
			services := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			before, after := placementUpdateServices(mode, 3)
			tasks := placementTasks(3, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")

			services.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).
				Return([]swarm.StackService{after}, nil)
			services.EXPECT().ListTasks(gomock.Any(), after.ID).Return(tasks, nil)
			nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{compatiblePlacementNode("node-east")}, nil)

			var progress ServiceProgress
			result, err := NewServiceWaiter(services, nodes, time.Millisecond, time.Second).Wait(
				t.Context(), placementWaitRequest(before, after, func(update ServiceProgress) { progress = update }),
			)

			require.NoError(t, err)
			assert.Empty(t, result.UnresolvedServices)
			assert.Equal(t, ServiceReady, progress.State)
			assert.Equal(t, uint64(3), progress.Running)
			assert.Nil(t, after.UpdateStatus, "placement-only convergence must not require rolling-update metadata")
			for index, task := range tasks {
				assert.Equal(t, "task-"+time.Duration(index+1).String(), task.ID)
				assert.Equal(t, before.CreatedAt, task.CreatedAt)
			}
		})
	}
}

func TestPlacementUpdateWaitsForIncompatibleTasksToBeReplaced(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		t.Run(mode, func(t *testing.T) {
			controller := gomock.NewController(t)
			services := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			before, after := placementUpdateServices(mode, 1)
			old := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-west")[0]
			replacement := placementTasks(1, after.ServiceSpec.TaskTemplate, after.UpdatedAt, "node-east")[0]
			replacement.ID = "replacement"
			stopping := old
			stopping.DesiredState = swarm.TaskStateShutdown

			gomock.InOrder(
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil),
				services.EXPECT().ListTasks(gomock.Any(), after.ID).Return([]swarm.ServiceTask{old}, nil),
				nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{incompatiblePlacementNode("node-west")}, nil),
				services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil),
				services.EXPECT().ListTasks(gomock.Any(), after.ID).
					Return([]swarm.ServiceTask{stopping, replacement}, nil),
			)

			var progress []ServiceProgress
			request := placementWaitRequest(before, after, func(update ServiceProgress) {
				progress = append(progress, update)
			})
			result, err := NewServiceWaiter(services, nodes, time.Millisecond, time.Second).Wait(t.Context(), request)

			require.NoError(t, err)
			assert.Empty(t, result.UnresolvedServices)
			require.Len(t, progress, 2)
			assert.Equal(t, ServicePending, progress[0].State)
			assert.Equal(t, uint64(0), progress[0].Running)
			assert.Equal(t, ServiceReady, progress[1].State)
			assert.Equal(t, uint64(1), progress[1].Running)
		})
	}
}

func TestPlacementAndImageUpdateStillRequiresNewTaskGeneration(t *testing.T) {
	controller := gomock.NewController(t)
	services := swarm.NewMockServiceManager(controller)
	nodes := swarm.NewMockNodeManager(controller)
	before, after := placementUpdateServices(replicatedServiceMode, 1)
	container := *after.ServiceSpec.TaskTemplate.ContainerSpec
	container.Image = "api:2"
	after.ServiceSpec.TaskTemplate.ContainerSpec = &container
	old := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")[0]
	completed := after
	completed.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStateCompleted, StartedAt: after.UpdatedAt}
	newTask := placementTasks(1, after.ServiceSpec.TaskTemplate, after.UpdatedAt, "node-east")[0]
	newTask.ID = "new-generation"
	stopping := old
	stopping.DesiredState = swarm.TaskStateShutdown

	gomock.InOrder(
		services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil),
		services.EXPECT().ListTasks(gomock.Any(), after.ID).Return([]swarm.ServiceTask{old}, nil),
		services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{completed}, nil),
		services.EXPECT().ListTasks(gomock.Any(), after.ID).Return([]swarm.ServiceTask{stopping, newTask}, nil),
	)

	var progress []ServiceProgress
	request := placementWaitRequest(before, after, func(update ServiceProgress) {
		progress = append(progress, update)
	})
	result, err := NewServiceWaiter(services, nodes, time.Millisecond, time.Second).Wait(t.Context(), request)

	require.NoError(t, err)
	assert.Empty(t, result.UnresolvedServices)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Equal(t, waitingForUpdateStatus, progress[0].Message)
	assert.Equal(t, ServiceReady, progress[1].State)
	assert.Equal(t, uint64(1), progress[1].Running)
}

func TestPlacementGenerationExemptionRemainsStrictForTaskReplacingChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*dockerswarm.ContainerSpec)
	}{
		{name: "image", edit: func(spec *dockerswarm.ContainerSpec) { spec.Image = "api:2" }},
		{name: "environment", edit: func(spec *dockerswarm.ContainerSpec) { spec.Env = []string{"MODE=new"} }},
		{name: "mounts", edit: func(spec *dockerswarm.ContainerSpec) {
			spec.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: "data", Target: "/data"}}
		}},
		{name: "secrets", edit: func(spec *dockerswarm.ContainerSpec) {
			spec.Secrets = []*dockerswarm.SecretReference{{SecretID: "secret-id"}}
		}},
		{name: "configs", edit: func(spec *dockerswarm.ContainerSpec) {
			spec.Configs = []*dockerswarm.ConfigReference{{ConfigID: "config-id"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := placementUpdateServices(replicatedServiceMode, 1)
			container := *after.ServiceSpec.TaskTemplate.ContainerSpec
			tc.edit(&container)
			after.ServiceSpec.TaskTemplate.ContainerSpec = &container

			assert.True(t, requiresTaskGeneration(before, after, false))
		})
	}
}

func TestUnresolvedPlacementUpdateCannotReuseOldHealthyTasks(t *testing.T) {
	controller := gomock.NewController(t)
	services := swarm.NewMockServiceManager(controller)
	nodes := swarm.NewMockNodeManager(controller)
	before, after := placementUpdateServices(replicatedServiceMode, 1)
	before.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: swarm.ServiceUpdateStatePaused, StartedAt: before.UpdatedAt,
	}
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")
	services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil).AnyTimes()
	services.EXPECT().ListTasks(gomock.Any(), after.ID).Return(tasks, nil).AnyTimes()
	request := placementWaitRequest(before, after, nil)
	request.UnresolvedServices = []string{"api"}

	result, err := NewServiceWaiter(services, nodes, time.Millisecond, 5*time.Millisecond).
		Wait(t.Context(), request)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, []string{"api"}, result.UnresolvedServices)
}

func TestPlacementUpdateWaitsForAcceptedSpecificationObservation(t *testing.T) {
	controller := gomock.NewController(t)
	services := swarm.NewMockServiceManager(controller)
	nodes := swarm.NewMockNodeManager(controller)
	before, after := placementUpdateServices(replicatedServiceMode, 1)
	stale := before
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")

	gomock.InOrder(
		services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{stale}, nil),
		services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil),
		services.EXPECT().ListTasks(gomock.Any(), after.ID).Return(tasks, nil),
		nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{compatiblePlacementNode("node-east")}, nil),
	)

	var progress []ServiceProgress
	request := placementWaitRequest(before, after, func(update ServiceProgress) {
		progress = append(progress, update)
	})
	result, err := NewServiceWaiter(services, nodes, time.Millisecond, time.Second).Wait(t.Context(), request)

	require.NoError(t, err)
	assert.Empty(t, result.UnresolvedServices)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Equal(t, "waiting for applied service version", progress[0].Message)
	assert.Equal(t, ServiceReady, progress[1].State)
}

func placementUpdateServices(mode string, desired uint64) (swarm.StackService, swarm.StackService) {
	var before swarm.StackService
	if mode == globalServiceMode {
		before = testGlobalService("api", "api-id", desired, serviceWaitTestTime.Add(-time.Minute))
	} else {
		before = testReplicatedService("api", "api-id", desired, serviceWaitTestTime.Add(-time.Minute), "api:1")
	}
	after := before
	after.UpdatedAt = serviceWaitTestTime
	after.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{
		"node.labels.zone == east",
		"node.role == manager",
		"node.platform.os == linux",
		"node.platform.arch == amd64",
		"engine.labels.storage == ssd",
		"node.ip == 10.0.0.0/24",
	}}
	previous := before.ServiceSpec
	after.PreviousSpec = &previous
	return before, after
}

func placementTasks(count int, template dockerswarm.TaskSpec, createdAt time.Time, node string) []swarm.ServiceTask {
	tasks := testRunningTasks(count, createdAt)
	for index := range tasks {
		tasks[index].Node = node
		tasks[index].Spec = swarm.NewTaskSpec(template)
	}
	return tasks
}

func compatiblePlacementNode(id string) swarm.Node {
	return swarm.Node{
		ID: id, Role: "manager", Hostname: "manager-1", PlatformOS: "linux", PlatformArchitecture: "amd64",
		Addr: "10.0.0.10", Labels: map[string]string{"zone": "east"}, EngineLabels: map[string]string{"storage": "ssd"},
	}
}

func incompatiblePlacementNode(id string) swarm.Node {
	node := compatiblePlacementNode(id)
	node.Labels = map[string]string{"zone": "west"}
	return node
}

func placementWaitRequest(
	before, after swarm.StackService,
	progress ServiceProgressCallback,
) ServiceWaitRequest {
	return ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, AppliedServices: []string{"api"},
		Baseline: []swarm.StackService{before}, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"api": after.ServiceSpec},
		Progress: progress,
	}
}
