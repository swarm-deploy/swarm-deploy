package waiter

import (
	"context"
	"errors"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
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

func TestWaitResultRetainsExhaustedTaskFailureForSameRevision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed bool
	}{
		{"same revision", false},
		{"new revision", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := serviceWaitTestTime
			api := testReplicatedService("api", "api-id", 1, now, "api:2")
			api.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			nextAPI := api
			if tc.changed {
				nextAPI.UpdatedAt = now.Add(time.Second)
			}
			worker := testReplicatedService("worker", "worker-id", 1, now, "worker:2")
			failed := testTask("failed-api-task", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			failed.Spec = swarm.NewTaskSpec(api.ServiceSpec.TaskTemplate)
			failed.Error = "unauthorized: authentication required"
			gomock.InOrder(
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{api, worker}, nil),
				manager.EXPECT().ListTasks(gomock.Any(), api.ID).Return([]swarm.ServiceTask{failed}, nil),
				manager.EXPECT().ListTasks(gomock.Any(), worker.ID).Return(nil, nil),
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{nextAPI, worker}, nil),
				manager.EXPECT().ListTasks(gomock.Any(), api.ID).Return(nil, temporaryDockerError()),
				manager.EXPECT().ListTasks(gomock.Any(), worker.ID).DoAndReturn(func(context.Context, string) ([]swarm.ServiceTask, error) {
					cancel()
					return nil, nil
				}),
			)
			result, err := newTestServiceWaiter(manager).Wait(ctx, ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api", "worker"},
			})
			require.ErrorIs(t, err, context.Canceled)
			if tc.changed {
				require.NotErrorIs(t, err, ErrConvergenceFailed)
				assert.Empty(t, result.FailedTasks)
				assert.Empty(t, faults.Causes(err))
				return
			}
			require.ErrorIs(t, err, ErrConvergenceFailed)
			require.Len(t, result.FailedTasks, 1)
			assert.Equal(t, failed.ID, result.FailedTasks[0].FailedTaskID)
			assert.Equal(t, []faults.Cause{{Code: faults.CodeImagePullDenied, Summary: failed.Error}}, faults.Causes(err))
		})
	}
}
