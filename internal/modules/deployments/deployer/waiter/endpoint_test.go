package waiter

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestEndpointRolloutVerification(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, tc := range []struct {
			name                                    string
			state                                   swarm.ServiceUpdateState
			historical, replacement, starting, zero bool
			want                                    ServiceState
		}{
			{name: "accepted without status", want: ServicePending},
			{name: "historical completed", state: swarm.ServiceUpdateStateCompleted, historical: true, want: ServicePending},
			{name: "historical paused", state: swarm.ServiceUpdateStatePaused, historical: true, want: ServicePending},
			{name: "new rollout old tasks", state: swarm.ServiceUpdateStateUpdating, want: ServicePending},
			{name: "replacement starting", state: swarm.ServiceUpdateStateUpdating, replacement: true, starting: true, want: ServicePending},
			{name: "running but updating", state: swarm.ServiceUpdateStateUpdating, replacement: true, want: ServicePending},
			{name: "completed but old tasks", state: swarm.ServiceUpdateStateCompleted, want: ServicePending},
			{name: "completed with replacement", state: swarm.ServiceUpdateStateCompleted, replacement: true, want: ServiceReady},
			{name: "paused", state: swarm.ServiceUpdateStatePaused, want: ServiceFailed},
			{name: "zero replicas without rollout", zero: true, want: ServiceReady},
		} {
			name := "apply/" + tc.name
			if recovery {
				name = "recovery/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Minute), "api:2")
				before.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8080}}}
				after := before
				after.UpdatedAt = serviceWaitTestTime.Add(time.Second)
				after.PreviousSpec = &before.ServiceSpec
				after.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8081}}}
				if tc.state != "" {
					started := serviceWaitTestTime
					if tc.historical {
						started = before.UpdatedAt.Add(-time.Minute)
					}
					after.UpdateStatus = &swarm.ServiceUpdateStatus{State: tc.state, StartedAt: started}
					if tc.historical {
						before.UpdateStatus = after.UpdateStatus
					}
				}
				tasks := testRunningTasks(1, before.CreatedAt)
				if tc.replacement {
					tasks = testRunningTasks(1, serviceWaitTestTime)
				}
				if tc.starting {
					tasks[0].CurrentState = swarm.TaskStateStarting
				}
				if tc.zero {
					n := uint64(0)
					after.Replicas = &n
					tasks = nil
				}
				generation := services.UpdateGeneration{ServiceID: before.ID, After: before.UpdatedAt}
				baseline := before
				if recovery {
					baseline = after
				}
				progress := evaluateService("api", baseline, after, tasks, recovery, after.ServiceSpec, nil, generation)
				assert.Equal(t, tc.want, progress.State, progress.Message)
				// External replicas use the same rollout attribution without requiring task health.
				external := specificationProgress("api", baseline, after, recovery, after.ServiceSpec, generation, nil)
				wantExternal := tc.want
				if tc.state == swarm.ServiceUpdateStateCompleted && !tc.historical {
					wantExternal = ServiceReady
				}
				assert.Equal(t, wantExternal, external.State, external.Message)
			})
		}
	}
}

func TestEndpointAndPlacementPreservesOnlyCompatibleTasks(t *testing.T) {
	before, after := placementUpdateServices(replicatedServiceMode, 1)
	before.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8080}}}
	after.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8081}}}
	after.PreviousSpec = &before.ServiceSpec
	tasks := placementTasks(1, before.ServiceSpec.TaskTemplate, before.CreatedAt, "node-east")
	for _, recovery := range []bool{false, true} {
		baseline := before
		if recovery {
			baseline = after
		}
		progress := evaluateTestService("api", baseline, after, tasks, recovery, after.ServiceSpec, placementNodesByID([]swarm.Node{compatiblePlacementNode("node-east")}))
		assert.Equal(t, ServiceReady, progress.State, progress.Message)
		progress = evaluateTestService("api", baseline, after, tasks, recovery, after.ServiceSpec, placementNodesByID([]swarm.Node{incompatiblePlacementNode("node-east")}))
		assert.Equal(t, ServicePending, progress.State, progress.Message)
	}
}

func TestLegacyEndpointRecoveryDoesNotTrustHistoricalCompletion(t *testing.T) {
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	previous := service.ServiceSpec
	service.PreviousSpec = &previous
	service.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8080}}}
	service.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStateCompleted, StartedAt: serviceWaitTestTime.Add(-time.Hour)}
	progress := evaluateTestService("api", service, service, testRunningTasks(1, serviceWaitTestTime.Add(-time.Minute)), true, service.ServiceSpec, nil)
	assert.Equal(t, ServicePending, progress.State)
	assert.Equal(t, waitingForUpdateStatus, progress.Message)
}

func TestEndpointRolloutSurvivesMetadataWrite(t *testing.T) {
	started := serviceWaitTestTime
	for _, tc := range []struct {
		name                              string
		state                             swarm.ServiceUpdateState
		historical, replacement, starting bool
		want                              ServiceState
	}{
		{name: "old tasks and absent status", want: ServicePending},
		{name: "old tasks restarted before port update", want: ServicePending},
		{name: "historical completion", state: swarm.ServiceUpdateStateCompleted, historical: true, replacement: true, want: ServicePending},
		{name: "replacement still starting", replacement: true, starting: true, want: ServicePending},
		{name: "replacement running without status", replacement: true, want: ServiceReady},
		{name: "replacement running while updating", state: swarm.ServiceUpdateStateUpdating, replacement: true, want: ServicePending},
		{name: "completed with replacement", state: swarm.ServiceUpdateStateCompleted, replacement: true, want: ServiceReady},
		{name: "paused", state: swarm.ServiceUpdateStatePaused, want: ServiceFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := testReplicatedService("api", "api-id", 1, started.Add(10*time.Second), "api:2")
			current.ServiceSpec.EndpointSpec = &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 8081}}}
			previous := current.ServiceSpec
			current.PreviousSpec = &previous // Docker has overwritten the original endpoint transition.
			current.ServiceSpec.Labels = map[string]string{"release": "metadata"}
			generation := services.UpdateGeneration{ServiceID: current.ID, Mode: services.VerifyNewGeneration,
				After: started.Add(5 * time.Second), TasksAfter: started}
			if tc.state != "" {
				statusStarted := started.Add(6 * time.Second)
				if tc.historical {
					statusStarted = started
				}
				current.UpdateStatus = &swarm.ServiceUpdateStatus{State: tc.state, StartedAt: statusStarted}
			}
			created := started.Add(-time.Minute)
			if tc.name == "old tasks restarted before port update" {
				created = started.Add(-time.Second)
			}
			if tc.replacement {
				created = started.Add(time.Second)
			} // Before the metadata write and its new rollout status.
			tasks := testRunningTasks(1, created)
			if tc.starting {
				tasks[0].CurrentState = swarm.TaskStateStarting
			}
			progress := evaluateService("api", current, current, tasks, true, current.ServiceSpec, nil, generation)
			assert.Equal(t, tc.want, progress.State, progress.Message)
			wantExternal := tc.want
			if tc.starting || (tc.state == swarm.ServiceUpdateStateCompleted && !tc.historical) {
				wantExternal = ServiceReady
			}
			external := specificationProgress("api", current, current, true, current.ServiceSpec, generation, tasks)
			assert.Equal(t, wantExternal, external.State, external.Message)
		})
	}
}

func TestExternalCreationRecoveryDoesNotRequireRolloutStatus(t *testing.T) {
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	for _, baseline := range []swarm.StackService{{}, service} {
		t.Run(baseline.ID, func(t *testing.T) {
			progress := specificationProgress("api", baseline, service, true, service.ServiceSpec, services.UpdateGeneration{}, nil)
			assert.Equal(t, ServiceReady, progress.State, "initial creation has no rolling update or task-health requirement")
		})
	}
}
