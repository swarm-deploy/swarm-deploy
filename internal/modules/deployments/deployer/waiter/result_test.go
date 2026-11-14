package waiter

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestWaitResultRetainsTargetsNeverObserved(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	cause := errors.New("permission denied")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, cause)
	result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"worker", "api"},
	})
	require.ErrorIs(t, err, cause)
	assert.Equal(t, []string{"api", "worker"}, result.UnresolvedServices)
}

func TestWaitResultIsIndependentOfProgressSubscription(t *testing.T) {
	for _, subscribe := range []bool{false, true} {
		t.Run(map[bool]string{false: "without callback", true: "with callback"}[subscribe], func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			failed := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			failed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: serviceWaitTestTime}
			healthy := testReplicatedService("worker", "worker-id", 1, serviceWaitTestTime, "worker:2")
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{failed, healthy}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{
				{ID: "failed-api-task", CurrentState: swarm.TaskStateFailed, CreatedAt: serviceWaitTestTime},
			}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "worker-id").Return(testRunningTasks(1, serviceWaitTestTime, healthy.ServiceSpec.TaskTemplate), nil)
			request := ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api", "worker"}}
			var progress []ServiceProgress
			if subscribe {
				request.Progress = func(p ServiceProgress) { progress = append(progress, p) }
			}
			result, err := newTestServiceWaiter(manager).Wait(t.Context(), request)
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.Equal(t, []string{"api"}, result.UnresolvedServices)
			require.Len(t, result.FailedTasks, 1)
			assert.Equal(t, "failed-api-task", result.FailedTasks[0].FailedTaskID)
			if subscribe {
				assert.Len(t, progress, 2)
			}
		})
	}
}

func TestWaitResultRetainsConfirmedTaskWhenLaterDiagnosticsAreUnavailable(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	failed := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	failed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: serviceWaitTestTime}
	healthy := testReplicatedService("worker", "worker-id", 1, serviceWaitTestTime, "worker:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{failed, healthy}, nil).Times(2)
	gomock.InOrder(
		manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{
			{ID: "failed-api-task", CurrentState: swarm.TaskStateFailed, CreatedAt: serviceWaitTestTime},
		}, nil),
		manager.EXPECT().ListTasks(gomock.Any(), "worker-id").Return(nil, nil),
		manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, errors.New("task history unavailable")),
		manager.EXPECT().ListTasks(gomock.Any(), "worker-id").Return(
			testRunningTasks(1, serviceWaitTestTime, healthy.ServiceSpec.TaskTemplate), nil),
	)
	result, err := NewServiceWaiter(manager, nil, time.Millisecond, time.Second).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"worker", "api"},
	})
	require.ErrorIs(t, err, ErrConvergenceFailed)
	require.Len(t, result.FailedTasks, 1)
	assert.Equal(t, "api", result.FailedTasks[0].Name)
	assert.Equal(t, "failed-api-task", result.FailedTasks[0].FailedTaskID)
}
