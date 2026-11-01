package waiter

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

var serviceWaitTestTime = time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC)

func TestInitialNonRestartingTaskFailureIsTerminal(t *testing.T) {
	exit := 23
	for _, tc := range []struct {
		name     string
		exitCode *int
	}{
		{name: "nonzero exit code", exitCode: &exit},
		{name: "exit code unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "busybox:1.36")
			service.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			task := testTask("failed-initial", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, serviceWaitTestTime.Add(time.Second))
			task.ExitCode = tc.exitCode
			task.Error = "task startup failed"
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil).AnyTimes()
			_, err := NewServiceWaiter(manager, time.Millisecond, 20*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"},
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.NotErrorIs(t, err, context.DeadlineExceeded)
			assert.ErrorContains(t, err, "id=failed-initial state=failed")
			assert.ErrorContains(t, err, task.Error)
			if tc.exitCode != nil {
				assert.ErrorContains(t, err, "exit_code=23")
			} else {
				assert.NotContains(t, err.Error(), "exit_code=")
			}
		})
	}
}

func TestServiceWaiterNewReplicatedServiceConvergesAndReportsTransitions(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 3, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(3, serviceWaitTestTime), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Equal(t, uint64(2), progress[0].Running)
	assert.Equal(t, ServiceReady, progress[1].State)
	assert.Equal(t, uint64(3), progress[1].Running)
}

func TestServiceWaiterRollingUpdateIgnoresOldTasks(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testReplicatedService("api", "api-id", 3, serviceWaitTestTime.Add(-time.Minute), "api:1")
	service := testReplicatedService("api", "api-id", 3, serviceWaitTestTime, "api:2")
	service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
	oldTasks := testRunningTasks(3, serviceWaitTestTime.Add(-time.Minute))
	firstTasks := append(oldTasks, testTask("new-1", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, serviceWaitTestTime))
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(firstTasks, nil)

	completed := service
	completed.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: "completed", StartedAt: serviceWaitTestTime, CompletedAt: serviceWaitTestTime.Add(time.Second),
	}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{completed}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(3, serviceWaitTestTime), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, uint64(1), progress[0].Running, "old specification tasks must not count")
	assert.Equal(t, ServiceReady, progress[1].State)
}

func TestServiceWaiterWaitsForUpdateStatusCompletion(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Minute), "api:1")
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)
	completed := service
	completed.UpdatedAt = serviceWaitTestTime.Add(2 * time.Second)
	completed.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: "completed", StartedAt: serviceWaitTestTime, CompletedAt: serviceWaitTestTime.Add(time.Second),
	}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{completed}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(
		testRunningTasks(1, serviceWaitTestTime.Add(500*time.Millisecond)), nil,
	)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
	})
	require.NoError(t, err)
}

func TestServiceWaiterAllowsFailedTaskReplacement(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	failed := testTask("failed", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, serviceWaitTestTime)
	failed.Error = "image pull failed"
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{failed}, nil)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime.Add(time.Second)), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.NotEmpty(t, progress)
	assert.Contains(t, progress[0].Message, "image pull failed")
	assert.Equal(t, ServiceReady, progress[len(progress)-1].State)
}

func TestServiceWaiterFailsTerminalUpdateStates(t *testing.T) {
	for _, state := range []string{"paused", "rollback_started", "rollback_paused", "rollback_completed"} {
		t.Run(state, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			service.UpdateStatus = &swarm.ServiceUpdateStatus{State: state, Message: "update failure"}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, nil)

			err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"},
			})
			require.Error(t, err)
			assert.ErrorContains(t, err, state)
		})
	}
}

func TestServiceWaiterReplicatedServiceScaledToZero(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 0, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{
		testTask("old", 1, swarm.TaskStateShutdown, swarm.TaskStateShutdown, serviceWaitTestTime.Add(-time.Minute)),
	}, nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}})
	require.NoError(t, err)
}

func TestServiceWaiterExistingZeroReplicasAcceptsTaskTemplateUpdateWithoutRollout(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testReplicatedService("api", "api-id", 0, serviceWaitTestTime.Add(-time.Minute), "api:1")
	service := testReplicatedService("api", "api-id", 0, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
	})
	require.NoError(t, err)
}

func TestServiceWaiterExistingUnchangedZeroReplicasConverges(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 0, serviceWaitTestTime, "api:1")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{service},
	})
	require.NoError(t, err)
}

func TestServiceWaiterScaleDownToZeroWaitsForOldTasksToStop(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Minute), "api:1")
	service := testReplicatedService("api", "api-id", 0, serviceWaitTestTime, "api:1")
	stopping := testTask("old", 1, swarm.TaskStateShutdown, swarm.TaskStateRunning, serviceWaitTestTime.Add(-time.Minute))
	stopped := stopping
	stopped.CurrentState = swarm.TaskStateShutdown
	stopped.UpdatedAt = serviceWaitTestTime.Add(time.Second)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{stopping}, nil)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{stopped}, nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Contains(t, progress[0].Message, "active tasks to stop")
	assert.Equal(t, ServiceReady, progress[1].State)
}

func TestServiceWaiterScalingConverges(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		before     uint64
		after      uint64
		active     int
		extraTasks []swarm.ServiceTask
	}{
		{name: "scale up", before: 1, after: 3, active: 3},
		{name: "scale down", before: 3, after: 1, active: 1, extraTasks: []swarm.ServiceTask{
			testTask("old-2", 2, swarm.TaskStateShutdown, swarm.TaskStateShutdown, serviceWaitTestTime.Add(-time.Minute)),
			testTask("old-3", 3, swarm.TaskStateShutdown, swarm.TaskStateShutdown, serviceWaitTestTime.Add(-time.Minute)),
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			baseline := testReplicatedService("api", "api-id", testCase.before, serviceWaitTestTime.Add(-time.Minute), "api:2")
			service := testReplicatedService("api", "api-id", testCase.after, serviceWaitTestTime, "api:2")
			tasks := append(testRunningTasks(testCase.active, serviceWaitTestTime.Add(-time.Minute)), testCase.extraTasks...)
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(tasks, nil)

			err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
			})
			require.NoError(t, err)
		})
	}
}

func TestServiceWaiterGlobalServiceConverges(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"agent"}})
	require.NoError(t, err)
}

func TestServiceWaiterNewGlobalServiceWaitsForSchedulerPropagation(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	propagating := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{propagating}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(nil, nil)

	converged := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{converged}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"agent"},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Equal(t, "waiting for global scheduler to create tasks", progress[0].Message)
	assert.Equal(t, ServiceReady, progress[1].State)
}

func TestServiceWaiterGlobalTasksOverrideLaggingServiceStatus(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	propagating := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime)
	pendingTasks := []swarm.ServiceTask{
		testTask("task-1", 0, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime),
		testTask("task-2", 0, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime),
	}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{propagating}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(pendingTasks, nil)

	converged := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{converged}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"agent"},
	})
	require.NoError(t, err)
}

func TestServiceWaiterExistingGlobalServiceWithZeroEligibleTasksConverges(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime.Add(-time.Minute))
	service := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime)
	service.ServiceSpec.TaskTemplate.ContainerSpec.Image = "agent:2"
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(nil, nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"agent"}, Baseline: []swarm.StackService{baseline},
	})
	require.NoError(t, err)
}

func TestServiceWaiterNewGlobalServiceWithZeroDesiredTasksDoesNotConvergePrematurely(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(nil, nil).AnyTimes()

	waiter := NewServiceWaiter(manager, time.Millisecond, 4*time.Millisecond)
	err := waitForTest(waiter, t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"agent"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "waiting for global scheduler to create tasks")
}

func TestServiceWaiterUpdatedGlobalServiceDoesNotConvergeBeforeSchedulerPropagation(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime.Add(-time.Minute))
	propagating := testGlobalService("agent", "agent-id", 0, serviceWaitTestTime)
	propagating.ServiceSpec.TaskTemplate.ContainerSpec.Image = "agent:2"
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{propagating}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(nil, nil)

	converged := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime)
	converged.ServiceSpec.TaskTemplate.ContainerSpec.Image = "agent:2"
	converged.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: "completed", StartedAt: serviceWaitTestTime, CompletedAt: serviceWaitTestTime.Add(time.Second),
	}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{converged}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"agent"}, Baseline: []swarm.StackService{baseline},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, ServicePending, progress[0].State)
	assert.Equal(t, ServiceReady, progress[1].State)
}

func TestServiceWaiterGlobalRollingUpdateConverges(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	baseline := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime.Add(-time.Minute))
	updating := testGlobalService("agent", "agent-id", 2, serviceWaitTestTime)
	updating.ServiceSpec.TaskTemplate.ContainerSpec.Image = "agent:2"
	updating.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{updating}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)

	completed := updating
	completed.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: "completed", StartedAt: serviceWaitTestTime, CompletedAt: serviceWaitTestTime.Add(time.Second),
	}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{completed}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "agent-id").Return(testRunningTasks(2, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"agent"}, Baseline: []swarm.StackService{baseline},
	})
	require.NoError(t, err)
}

func TestServiceWaiterHandlesInitialPropagationDelay(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)

	progress := make([]ServiceProgress, 0)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.NoError(t, err)
	require.Len(t, progress, 2)
	assert.Equal(t, "waiting for service to appear", progress[0].Message)
	assert.Equal(t, ServiceReady, progress[1].State)
}

func TestServiceWaiterTimeoutIncludesPartialConvergence(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	ready := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	pending := testReplicatedService("worker", "worker-id", 2, serviceWaitTestTime, "worker:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{ready, pending}, nil).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), "worker-id").Return(testRunningTasks(1, serviceWaitTestTime), nil).AnyTimes()

	progress := make([]ServiceProgress, 0)
	waiter := NewServiceWaiter(manager, time.Millisecond, 4*time.Millisecond)
	err := waitForTest(waiter, t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api", "worker"},
		Progress: func(update ServiceProgress) { progress = append(progress, update) },
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "worker (1/2 running")
	assert.Equal(t, ServiceFailed, progress[len(progress)-1].State)
}

func TestServiceWaiterPropagatesCancellation(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, nil)
	ctx, cancel := context.WithCancel(t.Context())

	err := waitForTest(newTestServiceWaiter(manager), ctx, ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
		Progress: func(ServiceProgress) { cancel() },
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestServiceWaiterSingleServiceIgnoresOtherServices(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	api := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	worker := testReplicatedService("worker", "worker-id", 2, serviceWaitTestTime, "worker:1")
	worker.RunningTasks = 0
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{api, worker}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
	})
	require.NoError(t, err)
}

func TestServiceWaiterReturnsServiceObservationErrors(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, errors.New("manager unavailable"))

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}})
	require.Error(t, err)
	assert.ErrorContains(t, err, "manager unavailable")
}

func TestServiceWaiterRetriesTemporaryListServicesError(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, temporaryDockerError())
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
	})
	require.NoError(t, err)
}

func TestServiceWaiterRetriesTemporaryListTasksError(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, temporaryDockerError())
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
	})
	require.NoError(t, err)
}

func TestServiceWaiterPermanentListTasksErrorIsNotRetried(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, errors.New("permission denied"))

	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"},
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "permission denied")
}

func TestServiceWaiterRepeatedTemporaryErrorsEndInTimeoutWithLatestDiagnostic(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, temporaryDockerError()).AnyTimes()

	waiter := NewServiceWaiter(manager, time.Millisecond, 4*time.Millisecond)
	err := waitForTest(waiter, t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "last Docker API error")
	assert.ErrorContains(t, err, "temporary manager connection failure")
}

func TestServiceWaiterContextCancellationDuringTemporaryErrorRetries(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	ctx, cancel := context.WithCancel(t.Context())
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, temporaryDockerError())
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, swarm.ListServicesFilter) ([]swarm.StackService, error) {
			cancel()
			return nil, temporaryDockerError()
		},
	)

	err := waitForTest(newTestServiceWaiter(manager), ctx, ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func temporaryDockerError() error {
	return &net.DNSError{Err: "temporary manager connection failure", IsTemporary: true}
}

func newTestServiceWaiter(manager swarm.ServiceManager) *ServiceWaiter {
	return NewServiceWaiter(manager, time.Millisecond, time.Second)
}

func testReplicatedService(
	name string,
	id string,
	replicas uint64,
	updatedAt time.Time,
	image string,
) swarm.StackService {
	replicasCopy := replicas
	return swarm.StackService{
		ID: id, Name: name, Stack: "app", FullName: "app_" + name, Mode: "replicated", Replicas: &replicasCopy,
		RunningTasks: replicas, DesiredTasks: replicas, CreatedAt: updatedAt, UpdatedAt: updatedAt,
		ServiceSpec: dockerswarm.ServiceSpec{
			Annotations:  dockerswarm.Annotations{Name: "app_" + name},
			TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: image}},
			Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &replicasCopy}},
		},
	}
}

func testGlobalService(name, id string, desired uint64, updatedAt time.Time) swarm.StackService {
	return swarm.StackService{
		ID: id, Name: name, Stack: "app", FullName: "app_" + name, Mode: "global",
		RunningTasks: desired, DesiredTasks: desired, CreatedAt: updatedAt, UpdatedAt: updatedAt,
		ServiceSpec: dockerswarm.ServiceSpec{
			Annotations:  dockerswarm.Annotations{Name: "app_" + name},
			TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "agent:1"}},
			Mode:         dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}},
		},
	}
}

func testRunningTasks(count int, createdAt time.Time) []swarm.ServiceTask {
	tasks := make([]swarm.ServiceTask, 0, count)
	for index := 1; index <= count; index++ {
		tasks = append(tasks, testTask("task-"+time.Duration(index).String(), index,
			swarm.TaskStateRunning, swarm.TaskStateRunning, createdAt))
	}
	return tasks
}

func testTask(
	id string,
	slot int,
	desired swarm.TaskState,
	current swarm.TaskState,
	createdAt time.Time,
) swarm.ServiceTask {
	return swarm.ServiceTask{
		ID: id, Slot: slot, DesiredState: desired, CurrentState: current,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func TestJobsWaitForCompletion(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		t.Run(mode, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := swarm.StackService{ID: "job-id", Name: "job", Mode: mode, DesiredTasks: 1, JobStatus: &dockerswarm.JobStatus{}}
			if mode == "replicated-job" {
				service.ServiceSpec.Mode.ReplicatedJob = &dockerswarm.ReplicatedJob{}
			}
			pending := manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			service.CompletedTasks = 1
			if mode == "global-job" {
				service.DesiredTasks = 0
			}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).After(pending).Return([]swarm.StackService{service}, nil)
			var progress []ServiceProgress
			err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"job"}, Progress: func(p ServiceProgress) { progress = append(progress, p) }})
			require.NoError(t, err)
			require.Len(t, progress, 2)
			assert.Equal(t, ServicePending, progress[0].State)
			assert.Equal(t, ServiceReady, progress[1].State)
		})
	}
}

func TestGlobalJobPartialCompletionIsNotReady(t *testing.T) {
	service := swarm.StackService{ID: "job", Mode: "global-job", DesiredTasks: 1, CompletedTasks: 1, JobStatus: &dockerswarm.JobStatus{}}
	progress := jobProgress(ServiceProgress{State: ServicePending}, swarm.StackService{}, service)
	assert.Equal(t, uint64(2), progress.Desired)
	assert.Equal(t, ServicePending, progress.State)
	service.DesiredTasks = 0
	progress = jobProgress(ServiceProgress{State: ServicePending}, swarm.StackService{}, service)
	assert.Equal(t, ServiceReady, progress.State)
}

// waitForTest keeps error-only scenarios focused on their existing convergence assertions.
func waitForTest(waiter *ServiceWaiter, ctx context.Context, request ServiceWaitRequest) error {
	_, err := waiter.Wait(ctx, request)
	return err
}
