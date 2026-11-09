package waiter

import (
	"errors"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestMetadataUpdateAfterPlacementUpdateReusesOriginalTask(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		t.Run(mode, func(t *testing.T) {
			controller := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			created, placed := placementUpdateServices(mode, 1)
			metadata := placed
			metadata.UpdatedAt = placed.UpdatedAt.Add(time.Minute)
			metadata.ServiceSpec.Labels = map[string]string{"release": "new"}
			metadata.PreviousSpec = &placed.ServiceSpec
			// Swarm preserves the original task and its specification across both writes.
			tasks := placementTasks(1, created.ServiceSpec.TaskTemplate, created.CreatedAt, "node-east")
			w := NewServiceWaiter(manager, nodes, time.Millisecond, time.Second)

			for _, step := range []struct {
				name          string
				before, after swarm.StackService
				verification  services.VerificationMode
			}{
				{"create", swarm.StackService{}, created, services.VerifyNewGeneration},
				{"placement", created, placed, services.VerifyPlacementCompatibleTasks},
				{"deploy labels", placed, metadata, services.VerifyExistingTasks},
			} {
				t.Run(step.name, func(t *testing.T) {
					manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{step.after}, nil)
					manager.EXPECT().ListTasks(gomock.Any(), created.ID).Return(tasks, nil)
					if step.name != "create" {
						nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{compatiblePlacementNode("node-east")}, nil)
					}
					assert.Equal(t, step.verification, serviceVerificationMode(step.before, step.after, false, services.UpdateGeneration{}))
					var progress ServiceProgress
					result, err := w.Wait(t.Context(), placementWaitRequest(step.before, step.after, func(update ServiceProgress) {
						progress = update
					}))

					require.NoError(t, err, "convergence must not require a replacement task or time out")
					assert.Empty(t, result.UnresolvedServices)
					assert.Equal(t, ServiceReady, progress.State)
					assert.Equal(t, uint64(1), progress.Running)
					assert.Equal(t, uint64(1), progress.Desired)
					assert.Nil(t, step.after.UpdateStatus, "no new rolling-update generation is needed")
				})
			}
		})
	}
}

func TestMetadataUpdateVerifiesPreservedTaskCompatibility(t *testing.T) {
	lookupErr := errors.New("node lookup unavailable")
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		for _, tc := range []struct {
			name      string
			exact     bool
			edit      func(*dockerswarm.TaskSpec)
			node      swarm.Node
			lookup    bool
			lookupErr error
			want      ServiceState
		}{
			{name: "compatible placement", node: compatiblePlacementNode("node-east"), lookup: true, want: ServiceReady},
			{name: "incompatible placement", node: incompatiblePlacementNode("node-east"), lookup: true, want: ServicePending},
			{name: "missing node", lookup: true, want: ServicePending},
			{name: "node lookup failure", lookup: true, lookupErr: lookupErr, want: ServicePending},
			{name: "different image", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Image = "api:old" }, want: ServicePending},
			{name: "different environment", edit: func(spec *dockerswarm.TaskSpec) { spec.ContainerSpec.Env = []string{"MODE=old"} }, want: ServicePending},
			{name: "outdated force update and placement", edit: func(spec *dockerswarm.TaskSpec) { spec.ForceUpdate-- }, want: ServicePending},
			{name: "outdated force update", exact: true, edit: func(spec *dockerswarm.TaskSpec) { spec.ForceUpdate-- }, want: ServicePending},
			{name: "exact match without node lookup", exact: true, want: ServiceReady},
			{name: "normalized match without node lookup", exact: true, edit: func(spec *dockerswarm.TaskSpec) {
				spec.Runtime = dockerswarm.RuntimeContainer
				spec.Resources = &dockerswarm.ResourceRequirements{}
			}, want: ServiceReady},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				controller := gomock.NewController(t)
				manager := swarm.NewMockServiceManager(controller)
				nodes := swarm.NewMockNodeManager(controller)
				original, before := placementUpdateServices(mode, 1)
				before.ServiceSpec.TaskTemplate.ForceUpdate = 1
				after := before
				after.UpdatedAt = before.UpdatedAt.Add(time.Minute)
				after.ServiceSpec.Labels = map[string]string{"release": "new"}
				after.PreviousSpec = &before.ServiceSpec
				template := after.ServiceSpec.TaskTemplate
				if !tc.exact {
					template.Placement = original.ServiceSpec.TaskTemplate.Placement
				}
				container := *template.ContainerSpec
				template.ContainerSpec = &container
				if tc.edit != nil {
					tc.edit(&template)
				}
				tasks := placementTasks(1, template, original.CreatedAt, "node-east")
				manager.EXPECT().ListTasks(gomock.Any(), after.ID).Return(tasks, nil)
				if tc.lookup {
					nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{tc.node}, tc.lookupErr)
				}

				progress, _, err := NewServiceWaiter(manager, nodes, time.Millisecond, time.Second).observeService(
					t.Context(), "api", before, after, false, after.ServiceSpec, services.UpdateGeneration{},
				)

				if tc.lookupErr != nil {
					require.ErrorIs(t, err, tc.lookupErr)
					assert.Contains(t, err.Error(), "list nodes for service api placement")
					assert.Contains(t, progress.Message, "list nodes:")
				} else {
					require.NoError(t, err)
				}
				assert.Equal(t, tc.want, progress.State, progress.Message)
				assert.Equal(t, uint64(1), progress.Desired)
				if tc.want == ServiceReady {
					assert.Equal(t, uint64(1), progress.Running)
				} else {
					assert.Zero(t, progress.Running)
				}
			})
		}
	}
}

func TestPlacementUpdateChecksNodesForMatchingTasks(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		for _, tc := range []struct {
			name string
			node swarm.Node
			want ServiceState
		}{
			{name: "compatible node", node: compatiblePlacementNode("node-east"), want: ServiceReady},
			{name: "node labels changed", node: incompatiblePlacementNode("node-east"), want: ServicePending},
			{name: "missing node", want: ServicePending},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				controller := gomock.NewController(t)
				manager := swarm.NewMockServiceManager(controller)
				nodes := swarm.NewMockNodeManager(controller)
				before, after := placementUpdateServices(mode, 1)
				// A task with the latest template can still be running after its node's labels change.
				tasks := placementTasks(1, after.ServiceSpec.TaskTemplate, after.UpdatedAt, "node-east")
				manager.EXPECT().ListTasks(gomock.Any(), after.ID).Return(tasks, nil)
				nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{tc.node}, nil)

				progress, _, err := NewServiceWaiter(manager, nodes, time.Millisecond, time.Second).observeService(
					t.Context(), "api", before, after, false, after.ServiceSpec, services.UpdateGeneration{},
				)

				require.NoError(t, err)
				assert.Equal(t, tc.want, progress.State, progress.Message)
				assert.Equal(t, uint64(1), progress.Desired)
				if tc.want == ServiceReady {
					assert.Equal(t, uint64(1), progress.Running)
				} else {
					assert.Zero(t, progress.Running)
				}
			})
		}
	}
}

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

func TestPlacementOnlyUpdateWaitsForExpectedServiceSpecification(t *testing.T) {
	before, after := placementUpdateServices(replicatedServiceMode, 1)
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")
	expected := after.ServiceSpec
	container := *expected.TaskTemplate.ContainerSpec
	container.Image = "api:unexpected"
	expected.TaskTemplate.ContainerSpec = &container

	progress := evaluateTestService(
		"api", before, after, tasks, false, expected,
		placementNodesByID([]swarm.Node{compatiblePlacementNode("node-east")}),
	)

	assert.Equal(t, ServicePending, progress.State)
	assert.Equal(t, "waiting for expected service specification", progress.Message)
	assert.Equal(t, uint64(0), progress.Running)
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

			assert.Equal(t, services.VerifyNewGeneration, services.ClassifyVerification(before.ServiceSpec, after.ServiceSpec))
		})
	}
}

func TestUnresolvedPlacementUpdateRecoversPreservedTasks(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		t.Run(mode, func(t *testing.T) {
			controller := gomock.NewController(t)
			services := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			before, accepted := placementUpdateServices(mode, 1)
			tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")
			services.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{accepted}, nil)
			services.EXPECT().ListTasks(gomock.Any(), accepted.ID).Return(tasks, nil)
			nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{compatiblePlacementNode("node-east")}, nil)
			request := placementWaitRequest(accepted, accepted, nil)
			request.AppliedServices = nil
			request.UnresolvedServices = []string{"api"}

			result, err := NewServiceWaiter(services, nodes, time.Millisecond, time.Second).
				Wait(t.Context(), request)

			require.NoError(t, err)
			assert.Empty(t, result.UnresolvedServices)
			assert.Nil(t, accepted.UpdateStatus)
		})
	}
}

func TestUnresolvedPlacementUpdateWaitsForCompatibleTasks(t *testing.T) {
	for _, mode := range []string{replicatedServiceMode, globalServiceMode} {
		t.Run(mode, func(t *testing.T) {
			before, accepted := placementUpdateServices(mode, 1)
			old := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-west")
			incompatibleNodes := placementNodesByID([]swarm.Node{incompatiblePlacementNode("node-west")})
			progress := evaluateTestService(
				"api", accepted, accepted, old, true, accepted.ServiceSpec, incompatibleNodes,
			)
			assert.Equal(t, ServicePending, progress.State)
			assert.Equal(t, uint64(0), progress.Running)

			replacement := placementTasks(1, accepted.ServiceSpec.TaskTemplate, accepted.UpdatedAt, "node-east")
			compatibleNodes := placementNodesByID([]swarm.Node{compatiblePlacementNode("node-east")})
			progress = evaluateTestService(
				"api", accepted, accepted, replacement, true, accepted.ServiceSpec, compatibleNodes,
			)
			assert.Equal(t, ServiceReady, progress.State, progress.Message)
			assert.Equal(t, uint64(1), progress.Running)
		})
	}
}

func TestUnresolvedTaskReplacingUpdateRejectsOldTasks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		placement bool
	}{
		{name: "image"},
		{name: "placement and image", placement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Minute), "api:1")
			accepted := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			if tc.placement {
				accepted.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{
					Constraints: []string{"node.labels.zone == east"},
				}
			}
			previous := before.ServiceSpec
			accepted.PreviousSpec = &previous
			old := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")

			progress := evaluateTestService("api", accepted, accepted, old, true, accepted.ServiceSpec, nil)

			assert.Equal(t, ServicePending, progress.State)
			assert.Equal(t, waitingForUpdateStatus, progress.Message)
		})
	}
}

func TestUnresolvedPlacementRecoveryRequiresCurrentSpecAndTaskTemplateEvidence(t *testing.T) {
	before, accepted := placementUpdateServices(replicatedServiceMode, 1)
	oldImage := before.ServiceSpec.TaskTemplate
	oldImage.ContainerSpec = &dockerswarm.ContainerSpec{Image: "api:0"}
	staleTask := placementTasks(1, oldImage, before.CreatedAt, "node-east")
	nodes := placementNodesByID([]swarm.Node{compatiblePlacementNode("node-east")})

	progress := evaluateTestService("api", accepted, accepted, staleTask, true, accepted.ServiceSpec, nodes)
	assert.Equal(t, ServicePending, progress.State, "PreviousSpec alone cannot prove task convergence")

	unexpected := accepted.ServiceSpec
	unexpected.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "api:unexpected"}
	progress = evaluateTestService("api", accepted, accepted,
		placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east"),
		true, unexpected, nodes)
	assert.Equal(t, ServicePending, progress.State)
	assert.Equal(t, "waiting for expected service specification", progress.Message)
}

func TestUnresolvedTaskReplacingUpdateRejectsStaleCompletedStatus(t *testing.T) {
	before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-2*time.Hour), "api:1")
	accepted := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	previous := before.ServiceSpec
	accepted.PreviousSpec = &previous
	accepted.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: swarm.ServiceUpdateStateCompleted, StartedAt: serviceWaitTestTime.Add(-time.Hour),
	}
	old := placementTasks(1, before.ServiceSpec.TaskTemplate, serviceWaitTestTime.Add(-30*time.Minute), "node-east")

	progress := evaluateTestService("api", accepted, accepted, old, true, accepted.ServiceSpec, nil)

	assert.Equal(t, ServicePending, progress.State)
	assert.Equal(t, uint64(0), progress.Running)
}

func TestUnresolvedUpdateStatesRemainStrict(t *testing.T) {
	for _, state := range []swarm.ServiceUpdateState{
		swarm.ServiceUpdateStatePaused,
		swarm.ServiceUpdateStateRollbackStarted,
		swarm.ServiceUpdateStateRollbackPaused,
		swarm.ServiceUpdateStateRollbackCompleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			before, accepted := placementUpdateServices(replicatedServiceMode, 1)
			accepted.UpdateStatus = &swarm.ServiceUpdateStatus{State: state, StartedAt: accepted.UpdatedAt}
			baseline := accepted
			expected := accepted.ServiceSpec
			if state == swarm.ServiceUpdateStateRollbackCompleted {
				expected.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "api:2"}
			}
			tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")

			progress := evaluateTestService("api", baseline, accepted, tasks, true, expected, nil)

			assert.Equal(t, ServiceFailed, progress.State)
		})
	}

	before, accepted := placementUpdateServices(replicatedServiceMode, 1)
	accepted.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: swarm.ServiceUpdateStateUpdating, StartedAt: accepted.UpdatedAt,
	}
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")
	nodes := placementNodesByID([]swarm.Node{compatiblePlacementNode("node-east")})
	progress := evaluateTestService("api", accepted, accepted, tasks, true, accepted.ServiceSpec, nodes)
	assert.Equal(t, ServicePending, progress.State)
}

func TestPlacementRoleConstraintsUseMappedNodeRole(t *testing.T) {
	before, accepted := placementUpdateServices(replicatedServiceMode, 1)
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "transitioning")
	for _, tc := range []struct {
		name string
		role dockerswarm.NodeRole
		want ServiceState
	}{
		{name: "manager", role: dockerswarm.NodeRoleManager, want: ServiceReady},
		{name: "worker", role: dockerswarm.NodeRoleWorker, want: ServicePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := compatiblePlacementNode("transitioning")
			node.Role = string(tc.role)
			progress := evaluateTestService(
				"api", accepted, accepted, tasks, true, accepted.ServiceSpec,
				placementNodesByID([]swarm.Node{node}),
			)
			assert.Equal(t, tc.want, progress.State, progress.Message)
		})
	}
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
