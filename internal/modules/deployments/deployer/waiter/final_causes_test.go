package waiter

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
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
		apiError  string
		lastState swarm.TaskState
		terminal  bool
	}{
		{"typed terminal failure", denied, swarm.TaskStateRejected, true},
		{"untyped terminal failure", "application exited", swarm.TaskStateRejected, true},
		{"recovered service", denied, swarm.TaskStateRunning, false},
		{"replaced task", denied, swarm.TaskStatePreparing, false},
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
			failedAPI.Error, failedAPI.Spec = tc.apiError, swarm.NewTaskSpec(api.ServiceSpec.TaskTemplate)
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
			causes := []faults.Cause{
				{Code: faults.CodeNoSuitableNode, Summary: scheduling},
				{Code: faults.CodeMountFailed, Summary: mount},
			}
			if tc.terminal {
				require.ErrorIs(t, err, ErrConvergenceFailed)
				if tc.apiError == denied {
					causes = append([]faults.Cause{{Code: faults.CodeImagePullDenied, Summary: denied}}, causes...)
				}
				assert.Equal(t, causes, faults.Causes(err))
				require.Len(t, result.FailedTasks, 1)
				assert.Equal(t, failedAPI.ID, result.FailedTasks[0].FailedTaskID)
				return
			}
			require.NotErrorIs(t, err, ErrConvergenceFailed)
			assert.Empty(t, result.FailedTasks)
			assert.Equal(t, causes, faults.Causes(err))
			assert.NotContains(t, err.Error(), denied)
			assert.NotContains(t, err.Error(), failedAPI.ID)
			assert.NotContains(t, err.Error(), staleWorker.ID)
		})
	}
}

func TestWaiterReportsUnreplacedReplicaCausesAtDeadline(t *testing.T) {
	const registry = "error from registry: unexpected response"
	const denied = "unauthorized: authentication required"
	const mount = "mount failed: volume data not accessible"
	pullCause := faults.Cause{Code: faults.CodeImagePullDenied, Summary: denied}
	for _, tc := range []struct {
		name          string
		firstFailure  string
		secondFailure string
		want          []faults.Cause
	}{
		{"newer untyped failure", denied, "application exited", []faults.Cause{pullCause}},
		{"newer typed failure", denied, mount, []faults.Cause{{Code: faults.CodeMountFailed, Summary: mount}, pullCause}},
		{"temporary registry failure with permanent mount failure", registry, mount, []faults.Cause{
			{Code: faults.CodeMountFailed, Summary: mount},
			{Code: faults.CodeImagePullFailed, Summary: "Docker could not pull the image.", Temporary: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := serviceWaitTestTime
			service := testReplicatedService("api", "api-id", 3, now, "api:2")
			first := testTask("denied", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			first.Error = tc.firstFailure
			first.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
			second := testTask("second", 2, swarm.TaskStateShutdown, swarm.TaskStateFailed, now.Add(time.Second))
			second.Error = tc.secondFailure
			replaced := testTask("replaced", 3, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			replaced.Error = "mount failed: replaced volume"
			replaced.UpdatedAt = now.Add(3 * time.Second)
			replacement := testTask("replacement", 3, swarm.TaskStateRunning, swarm.TaskStatePreparing, now.Add(2*time.Second))
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).
				Return([]swarm.ServiceTask{replacement, first, replaced, second}, nil).MinTimes(1)

			result, err := NewServiceWaiter(manager, nil, time.Millisecond, 50*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{service.Name},
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NotErrorIs(t, err, ErrConvergenceFailed)
			assert.Empty(t, result.FailedTasks)
			assert.Equal(t, tc.want, faults.Causes(err))
			assert.NotContains(t, err.Error(), replaced.Error)
		})
	}
}

func TestWaiterKeepsPullDenialWhenAnotherServiceFails(t *testing.T) {
	const denied = "unauthorized: authentication required"
	const mount = "mount failed: volume data"
	pullCause := faults.Cause{Code: faults.CodeImagePullDenied, Summary: denied}
	mountCause := faults.Cause{Code: faults.CodeMountFailed, Summary: mount}
	listErr := errors.New("Docker API refused task listing")
	for _, tc := range []struct {
		name    string
		listErr error
		want    []faults.Cause
	}{
		{name: "terminal task failure", want: []faults.Cause{mountCause, pullCause}},
		{name: "observation failure", listErr: listErr, want: []faults.Cause{pullCause}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				now := serviceWaitTestTime
				api := testReplicatedService("api", "api-id", 1, now, "api:2")
				worker := testReplicatedService("worker", "worker-id", 1, now, "worker:2")
				worker.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{
					Condition: dockerswarm.RestartPolicyConditionNone,
				}
				deniedTask := testTask("api-rejected", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
				deniedTask.Error, deniedTask.Spec = denied, swarm.NewTaskSpec(api.ServiceSpec.TaskTemplate)
				failedTask := testTask("worker-rejected", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
				failedTask.Error, failedTask.Spec = mount, swarm.NewTaskSpec(worker.ServiceSpec.TaskTemplate)
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).
					Return([]swarm.StackService{worker, api}, nil).AnyTimes()
				manager.EXPECT().ListTasks(gomock.Any(), api.ID).Return([]swarm.ServiceTask{deniedTask}, nil).AnyTimes()
				if tc.listErr != nil {
					manager.EXPECT().ListTasks(gomock.Any(), worker.ID).Return(nil, tc.listErr)
				} else {
					manager.EXPECT().ListTasks(gomock.Any(), worker.ID).
						Return([]swarm.ServiceTask{failedTask}, nil).AnyTimes()
				}

				_, err := NewServiceWaiter(manager, nil, time.Millisecond, 20*time.Millisecond).Wait(t.Context(),
					ServiceWaitRequest{StackName: "app", ServiceNames: []string{worker.Name, api.Name}})
				if tc.listErr != nil {
					require.ErrorIs(t, err, tc.listErr)
					assert.NotErrorIs(t, err, ErrConvergenceFailed)
				} else {
					require.ErrorIs(t, err, ErrConvergenceFailed)
					assert.ErrorIs(t, err, context.DeadlineExceeded)
				}
				assert.Equal(t, tc.want, faults.Causes(err))
				assert.ErrorContains(t, err, denied)
			})
		})
	}
}

func TestWaiterCollectsObservationErrorsAndTaskFailuresInAnyOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		temporaryName string
		permanentName string
		failedName    string
		taskError     string
		wantCode      faults.Code
	}{
		{"temporary error first", "api", "sidecar", "worker", "mount failed: volume data", faults.CodeMountFailed},
		{"permanent error first", "worker", "api", "sidecar", "mount failed: volume data", faults.CodeMountFailed},
		{"task failure first", "sidecar", "worker", "api", "mount failed: volume data", faults.CodeMountFailed},
		{"temporary error first without typed cause", "api", "sidecar", "worker", "application exited", ""},
		{"permanent error first without typed cause", "worker", "api", "sidecar", "application exited", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := serviceWaitTestTime
			temporary := testReplicatedService(tc.temporaryName, "temp-id", 1, now, "app:2")
			permanent := testReplicatedService(tc.permanentName, "perm-id", 1, now, "app:2")
			failed := testReplicatedService(tc.failedName, "failed-id", 1, now, "app:2")
			failed.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateStatePaused, StartedAt: now}
			task := testTask("failed-task", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, now)
			task.Error = tc.taskError
			temporaryErr := temporaryDockerError()
			permanentErr := errors.New("permission denied")
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{failed, temporary, permanent}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), temporary.ID).Return(nil, temporaryErr)
			manager.EXPECT().ListTasks(gomock.Any(), permanent.ID).Return(nil, permanentErr)
			manager.EXPECT().ListTasks(gomock.Any(), failed.ID).Return([]swarm.ServiceTask{task}, nil)

			result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{failed.Name, permanent.Name, temporary.Name},
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.ErrorIs(t, err, temporaryErr)
			assert.ErrorIs(t, err, permanentErr)
			var wantCauses []faults.Cause
			if tc.wantCode != "" {
				wantCauses = append(wantCauses, faults.Cause{Code: tc.wantCode, Summary: task.Error})
			}
			assert.Equal(t, wantCauses, faults.Causes(err))
			safe, ok := faults.SafeError(err).(faults.Error)
			require.True(t, ok)
			assert.False(t, safe.Temporary())
			require.Len(t, result.FailedTasks, 1)
			assert.Equal(t, task.ID, result.FailedTasks[0].FailedTaskID)
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
