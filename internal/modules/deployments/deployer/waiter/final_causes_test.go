package waiter

import (
	"context"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestWaiterReportsLatestCausesAtDeadline(t *testing.T) {
	const denied = "unauthorized: authentication required"
	const mount = "mount failed: volume data not accessible"
	const scheduling = "no suitable node (insufficient resources on 1 node)"
	for _, tc := range []struct {
		name      string
		lastState swarm.TaskState
		terminal  bool
	}{
		{"confirmed failure", swarm.TaskStateRejected, true},
		{"recovered service", swarm.TaskStateRunning, false},
		{"replaced task", swarm.TaskStatePreparing, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := serviceWaitTestTime
			api := testReplicatedService("api", "api-id", 1, now, "api:2")
			api.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			worker := testReplicatedService("worker", "worker-id", 2, now, "worker:2")
			scheduler := testReplicatedService("scheduler", "scheduler-id", 1, now, "scheduler:2")
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{worker, scheduler, api}, nil).AnyTimes()
			failedAPI := testTask("api-original", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			failedAPI.Error, failedAPI.Spec = denied, swarm.NewTaskSpec(api.ServiceSpec.TaskTemplate)
			first := manager.EXPECT().ListTasks(gomock.Any(), api.ID).Return([]swarm.ServiceTask{failedAPI}, nil)
			apiTasks := []swarm.ServiceTask{failedAPI}
			if !tc.terminal {
				replacement := testTask("api-replacement", 1, swarm.TaskStateRunning, tc.lastState, now.Add(time.Second))
				replacement.Spec = failedAPI.Spec
				apiTasks = append(apiTasks, replacement)
			}
			manager.EXPECT().ListTasks(gomock.Any(), api.ID).After(first).Return(apiTasks, nil).MinTimes(1)
			failedWorker := testTask("worker-first", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			failedWorker.Error, failedWorker.Spec = mount, swarm.NewTaskSpec(worker.ServiceSpec.TaskTemplate)
			staleWorker := testTask("worker-old-generation", 2, swarm.TaskStateShutdown, swarm.TaskStateRejected, now.Add(-time.Second))
			staleWorker.Error, staleWorker.Spec = denied, failedWorker.Spec
			staleWorker.UpdatedAt = now.Add(time.Second)
			manager.EXPECT().ListTasks(gomock.Any(), worker.ID).Return([]swarm.ServiceTask{staleWorker, failedWorker}, nil).AnyTimes()
			pending := testTask("scheduler-first", 1, swarm.TaskStateRunning, swarm.TaskStatePending, now)
			pending.Error, pending.Spec = scheduling, swarm.NewTaskSpec(scheduler.ServiceSpec.TaskTemplate)
			manager.EXPECT().ListTasks(gomock.Any(), scheduler.ID).Return([]swarm.ServiceTask{pending}, nil).AnyTimes()

			result, err := NewServiceWaiter(manager, nil, time.Millisecond, 50*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"worker", "scheduler", "api"},
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			if tc.terminal {
				require.ErrorIs(t, err, ErrConvergenceFailed)
				assert.Equal(t, []faults.Cause{{Code: faults.CodeImagePullDenied, Summary: denied}}, faults.Causes(err))
				require.Len(t, result.FailedTasks, 1)
				assert.Equal(t, failedAPI.ID, result.FailedTasks[0].FailedTaskID)
				return
			}
			require.NotErrorIs(t, err, ErrConvergenceFailed)
			assert.Empty(t, result.FailedTasks)
			assert.Equal(t, []faults.Cause{
				{Code: faults.CodeNoSuitableNode, Summary: scheduling},
				{Code: faults.CodeMountFailed, Summary: mount},
			}, faults.Causes(err))
			assert.NotContains(t, err.Error(), denied)
			assert.NotContains(t, err.Error(), failedAPI.ID)
			assert.NotContains(t, err.Error(), staleWorker.ID)
		})
	}
}

func TestWaiterReportsAllConfirmedServiceFailures(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	now := serviceWaitTestTime
	api := testReplicatedService("api", "api-id", 1, now, "api:2")
	worker := testReplicatedService("worker", "worker-id", 1, now, "worker:2")
	status := &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStatePaused, StartedAt: now}
	api.UpdateStatus, worker.UpdateStatus = status, status
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{worker, api}, nil)
	for _, tc := range []struct{ id, diagnostic string }{
		{api.ID, "unauthorized: authentication required"},
		{worker.ID, "mount failed: volume data"},
	} {
		task := testTask(tc.id+"-task", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
		task.Error = tc.diagnostic
		manager.EXPECT().ListTasks(gomock.Any(), tc.id).Return([]swarm.ServiceTask{task}, nil)
	}

	result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"worker", "api"},
	})
	require.ErrorIs(t, err, ErrConvergenceFailed)
	require.Len(t, result.FailedTasks, 2)
	assert.Equal(t, []faults.Cause{
		{Code: faults.CodeImagePullDenied, Summary: "unauthorized: authentication required"},
		{Code: faults.CodeMountFailed, Summary: "mount failed: volume data"},
	}, faults.Causes(err))
}
