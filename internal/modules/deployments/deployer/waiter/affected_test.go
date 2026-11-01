package waiter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestAppliedServiceFailureCannotBeHiddenByOldRunningTask(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
	after := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: serviceWaitTestTime, Message: "update paused due to failure of task failed-task"}
	failed := testTask("failed-task", 1, swarm.TaskStateShutdown, swarm.TaskStateFailed, serviceWaitTestTime)
	exitCode := 1
	failed.ExitCode = &exitCode
	failed.Error = "task: non-zero exit (1)"
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(append(testRunningTasks(1, serviceWaitTestTime.Add(-time.Hour)), failed), nil)
	err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}, AppliedServices: []string{"api"}, Baseline: []swarm.StackService{before}})
	require.ErrorIs(t, err, ErrConvergenceFailed)
	for _, detail := range []string{"api", "paused", "failed-task", "exit_code=1", "non-zero exit"} {
		assert.Contains(t, err.Error(), detail)
	}
}

func TestAppliedTargetsExcludeUnrelatedHistoricalFailure(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
	after := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
	unrelated := testReplicatedService("other", "other-id", 1, serviceWaitTestTime.Add(-time.Hour), "other:1")
	unrelated.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused"}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after, unrelated}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)
	require.NoError(t, newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}, AppliedServices: []string{"api"}, Baseline: []swarm.StackService{before, unrelated}}))
}

func TestAppliedTargetWaitsForFreshServiceAndUpdateStatus(t *testing.T) {
	for _, historicalState := range []string{"paused", "completed"} {
		t.Run(historicalState, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			before := testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
			before.UpdateStatus = &swarm.ServiceUpdateStatus{State: historicalState, StartedAt: serviceWaitTestTime.Add(-time.Hour)}
			after := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			after.UpdateStatus = before.UpdateStatus
			// The first observation has not seen the accepted spec yet. The second has
			// seen the spec but Swarm still reports the old update's outcome.
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{before}, nil)
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil)
			if historicalState == "completed" {
				manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime.Add(-time.Hour)), nil)
			}
			ready := after
			ready.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{ready}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)
			var progress []ServiceProgress
			err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}, AppliedServices: []string{"api"}, Baseline: []swarm.StackService{before}, Progress: func(p ServiceProgress) { progress = append(progress, p) }})
			require.NoError(t, err)
			require.Len(t, progress, 3)
			assert.Equal(t, ServicePending, progress[0].State)
			assert.Equal(t, ServicePending, progress[1].State)
			assert.Equal(t, ServiceReady, progress[2].State)
		})
	}
}

func TestNoAppliedServicesIsSuccessfulNoop(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	require.NoError(t, newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{StackName: "app", AppliedServices: []string{}}))
}

func TestUnresolvedRolloutNeedsTasksFromDesiredGeneration(t *testing.T) {
	for _, applied := range []bool{false, true} {
		for _, staleObservation := range []bool{false, true} {
			t.Run(fmt.Sprint(staleObservation, applied), func(t *testing.T) {
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
				baseline.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: serviceWaitTestTime}
				completed := baseline
				completed.UpdatedAt = serviceWaitTestTime.Add(time.Minute)
				completed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
				first := completed
				if staleObservation {
					first = testReplicatedService("api", "api-id", 1, serviceWaitTestTime.Add(-time.Hour), "api:1")
					first.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime.Add(-time.Hour)}
				}
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{first}, nil)
				manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime.Add(-time.Hour)), nil)
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{completed}, nil)
				manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime), nil)
				var appliedNames []string
				if applied {
					appliedNames = []string{"api"}
				}
				var states []ServiceState
				err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
					AppliedServices: appliedNames,
					StackName:       "app", ServiceNames: []string{"api"}, UnresolvedServices: []string{"api"}, Baseline: []swarm.StackService{baseline},
					Progress: func(p ServiceProgress) { states = append(states, p.State) },
				})
				require.NoError(t, err)
				assert.Equal(t, []ServiceState{ServicePending, ServiceReady}, states)
			})
		}
	}

}

func TestTerminalFailureDoesNotPreventIndependentTargetsFromFinishing(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	failed := testReplicatedService("a", "a-id", 1, serviceWaitTestTime, "a:2")
	failed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: serviceWaitTestTime}
	pending := testReplicatedService("b", "b-id", 1, serviceWaitTestTime, "b:2")
	pending.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
	completed := pending
	completed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{failed, pending}, nil)
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{failed, completed}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "a-id").Return(nil, nil).Times(2)
	manager.EXPECT().ListTasks(gomock.Any(), "b-id").Return(testRunningTasks(1, serviceWaitTestTime), nil).Times(2)
	var bStates []ServiceState
	err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"a", "b"}, UnresolvedServices: []string{"a"}, Baseline: []swarm.StackService{failed, pending},
		Progress: func(p ServiceProgress) {
			if p.Name == "b" {
				bStates = append(bStates, p.State)
			}
		},
	})
	require.ErrorIs(t, err, ErrConvergenceFailed)
	assert.Equal(t, []ServiceState{ServicePending, ServiceReady}, bStates)
}

func TestUnresolvedGenerationWithoutUpdateMetadataCannotSucceed(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	accepted := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{accepted}, nil).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(testRunningTasks(1, serviceWaitTestTime.Add(-time.Hour)), nil).AnyTimes()
	err := NewServiceWaiter(manager, time.Millisecond, 5*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"api"}, UnresolvedServices: []string{"api"}, Baseline: []swarm.StackService{accepted},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
