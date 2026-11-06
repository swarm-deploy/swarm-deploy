package waiter

import (
	"context"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	swarmkitapi "github.com/moby/swarmkit/v2/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func evaluateTestService(
	name string,
	baseline swarm.StackService,
	service swarm.StackService,
	tasks []swarm.ServiceTask,
	unresolved bool,
	expected dockerswarm.ServiceSpec,
	placementNodes map[string]*swarmkitapi.Node,
) ServiceProgress {
	return evaluateService(name, baseline, service, tasks, unresolved, expected, placementNodes, services.UpdateGeneration{})
}

func TestNewGenerationVerifiesExpectedSpecification(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		missingSpec bool
		want        ServiceState
	}{
		{name: "another operator changed the image", image: "api:3", want: ServicePending},
		{name: "matching specification and healthy tasks", image: "api:2", want: ServiceReady},
		{name: "expected specification unavailable", image: "api:3", missingSpec: true, want: ServiceReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
			expected := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2").ServiceSpec
			if tc.missingSpec {
				expected = dockerswarm.ServiceSpec{}
			}
			observed := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, tc.image)
			observed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			tasks := testRunningTasks(1, serviceWaitTestTime, observed.ServiceSpec.TaskTemplate)

			progress := evaluateService("api", baseline, observed, tasks, false, expected, nil, services.UpdateGeneration{
				ServiceID: "api-id", Mode: services.VerifyNewGeneration, After: baseline.UpdatedAt,
			})
			assert.Equal(t, tc.want, progress.State, progress.Message)
			if tc.want == ServicePending {
				assert.Contains(t, progress.Message, "expected service specification")
			}
		})
	}
}

func TestServiceWaiterTerminalTaskFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		global      bool
		maxAttempts uint64
		status      swarm.ServiceUpdateState
	}{
		{name: "global restart disabled", global: true},
		{name: "replicated retries exhausted", maxAttempts: 2},
		{name: "global retries exhausted", global: true, maxAttempts: 2},
		{name: "restart disabled before update pauses", status: swarm.ServiceUpdateStateUpdating},
		{name: "restart disabled after completed update", status: swarm.ServiceUpdateStateCompleted},
		{name: "retries exhausted after completed update", maxAttempts: 2, status: swarm.ServiceUpdateStateCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			if tc.global {
				service = testGlobalService("api", "api-id", 1, serviceWaitTestTime)
			}
			policy := &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			if tc.maxAttempts > 0 {
				policy.Condition = dockerswarm.RestartPolicyConditionOnFailure
				policy.MaxAttempts = &tc.maxAttempts
			}
			service.ServiceSpec.TaskTemplate.RestartPolicy = policy
			if tc.status != "" {
				service.UpdateStatus = &swarm.ServiceUpdateStatus{State: tc.status, StartedAt: serviceWaitTestTime}
			}
			exit := 23
			var tasks []swarm.ServiceTask
			for attempt := uint64(0); attempt <= tc.maxAttempts; attempt++ {
				task := testTask("attempt-"+time.Duration(attempt).String(), 1,
					swarm.TaskStateShutdown, swarm.TaskStateFailed, serviceWaitTestTime.Add(time.Duration(attempt)*time.Second))
				task.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
				task.Node, task.ExitCode, task.Error = "node-a", &exit, "task startup failed"
				tasks = append(tasks, task)
			}
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return(tasks, nil).AnyTimes()
			var progress []ServiceProgress
			result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"api": service.ServiceSpec},
				Progress:      func(p ServiceProgress) { progress = append(progress, p) },
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.NotErrorIs(t, err, context.DeadlineExceeded)
			require.Len(t, progress, 1)
			assert.Equal(t, ServiceFailed, progress[0].State)
			failed := tasks[len(tasks)-1]
			assert.Equal(t, failed.ID, progress[0].FailedTaskID)
			assert.ErrorContains(t, err, "id="+failed.ID+" state=failed exit_code=23")
			assert.ErrorContains(t, err, failed.Error)
			require.Len(t, result.FailedTasks, 1)
			assert.Equal(t, failed.ID, result.FailedTasks[0].FailedTaskID)
		})
	}
}

func TestWaiterWaitsForRecoverableOrHistoricalFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		maxAttempts uint64
		replacement swarm.TaskState
		historical  bool
		oldTemplate bool
	}{
		{name: "restart attempts remaining", maxAttempts: 2},
		{name: "replacement still pending", replacement: swarm.TaskStatePending},
		{name: "historical failure before cutoff", historical: true},
		{name: "historical task template", oldTemplate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			policy := &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			if tc.maxAttempts > 0 {
				policy.Condition, policy.MaxAttempts = dockerswarm.RestartPolicyConditionOnFailure, &tc.maxAttempts
			}
			service.ServiceSpec.TaskTemplate.RestartPolicy = policy
			failed := testTask("failed", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, serviceWaitTestTime)
			failed.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
			if tc.historical {
				failed.CreatedAt = serviceWaitTestTime.Add(-time.Minute)
			}
			if tc.oldTemplate {
				failed.Spec = swarm.NewTaskSpec(baseline.ServiceSpec.TaskTemplate)
			}
			tasks := []swarm.ServiceTask{failed}
			if tc.replacement != "" {
				replacement := testTask("replacement", 1, swarm.TaskStateRunning, tc.replacement, serviceWaitTestTime.Add(time.Second))
				replacement.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
				tasks = append(tasks, replacement)
			}
			progress := evaluateTestService("api", baseline, service, tasks, false, service.ServiceSpec, nil)
			assert.Equal(t, ServicePending, progress.State, progress.Message)
			assert.Empty(t, progress.FailedTaskID)
		})
	}
}

func TestJobsVerifyExpectedSpecification(t *testing.T) {
	for _, mode := range []string{replicatedJobServiceMode, globalJobServiceMode} {
		for _, tc := range []struct {
			name        string
			image       string
			missingSpec bool
			want        ServiceState
		}{
			{name: "unexpected image", image: "api:3", want: ServicePending},
			{name: "matching image", image: "api:2", want: ServiceReady},
			{name: "expected specification unavailable", image: "api:3", missingSpec: true, want: ServiceReady},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				service := swarm.StackService{
					ID: "job-id", Name: "job", Mode: mode, CompletedTasks: 1,
					JobStatus: &dockerswarm.JobStatus{},
				}
				service.ServiceSpec.Name = "job"
				service.ServiceSpec.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: tc.image}
				if mode == replicatedJobServiceMode {
					service.ServiceSpec.Mode.ReplicatedJob = &dockerswarm.ReplicatedJob{}
				} else {
					service.ServiceSpec.Mode.GlobalJob = &dockerswarm.GlobalJob{}
				}
				expected := service.ServiceSpec
				if tc.missingSpec {
					expected = dockerswarm.ServiceSpec{}
				} else {
					expected.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "api:2"}
				}
				progress := evaluateTestService("job", swarm.StackService{}, service, nil, false, expected, nil)
				assert.Equal(t, tc.want, progress.State, progress.Message)
				if tc.want == ServicePending {
					assert.Contains(t, progress.Message, "expected service specification")
				}
			})
		}
	}
}
