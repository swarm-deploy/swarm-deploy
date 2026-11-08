package waiter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestServiceWaiterTerminalTaskFaults(t *testing.T) {
	for _, tc := range []struct {
		state      swarm.TaskState
		diagnostic string
		code       faults.Code
		external   bool
	}{
		{swarm.TaskStatePending, "no suitable node (scheduling constraints not satisfied on 1 node)", faults.CodeNoSuitableNode, false},
		{swarm.TaskStateRejected, "unauthorized: authentication required", faults.CodeImagePullDenied, false},
		{swarm.TaskStateRejected, `invalid mount config for type "bind": bind source path does not exist: /srv/data`, faults.CodeMountFailed, false},
		{swarm.TaskStateRejected, "VolumeDriver.Mount: permission denied", faults.CodeMountFailed, true},
		{swarm.TaskStateRejected, "mount failed: " + strings.Repeat("backend unavailable; ", 30), faults.CodeMountFailed, false},
		{swarm.TaskStateRejected, "No such image: api:2", faults.CodeImageNotFound, false},
		{swarm.TaskStateFailed, "container startup failed", "", false},
	} {
		t.Run(string(tc.code)+"/"+tc.diagnostic, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			if tc.external {
				service = externalService(1, serviceWaitTestTime, "api:2")
			}
			service.UpdateStatus = &swarm.ServiceUpdateStatus{
				State: "paused", Message: "update paused due to failure of task current-task", StartedAt: serviceWaitTestTime,
			}
			task := testTask("current-task", 1, swarm.TaskStateShutdown, tc.state, serviceWaitTestTime)
			task.Error = tc.diagnostic
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil)
			var original faults.Coded
			err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{service.Name},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{service.Name: service.ServiceSpec},
				Progress:      func(p ServiceProgress) { original = p.taskFault },
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.ErrorContains(t, err, "update paused due to failure of task current-task")
			assert.ErrorContains(t, err, tc.diagnostic)
			assert.Equal(t, 1, strings.Count(err.Error(), tc.diagnostic))
			assert.Equal(t, 1, strings.Count(err.Error(), strings.SplitN(tc.diagnostic, ":", 2)[0]))
			var coded faults.Coded
			if tc.code == "" {
				assert.NotErrorAs(t, err, &coded)
				return
			}
			require.ErrorAs(t, err, &coded)
			assert.Same(t, original, coded)
			assert.Equal(t, tc.code, coded.FaultCode())
			assert.Equal(t, boundedDiagnostic(tc.diagnostic), coded.FaultSummary())
			assert.Equal(t, tc.code == faults.CodeImageNotFound, errors.Is(err, swarm.ErrImageNotFound))
		})
	}
}

func TestWaitInterruptedErrorPreservesTechnicalChain(t *testing.T) {
	task := testTask("pending-task", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
	task.Error = "no suitable node (insufficient resources on 1 node)"
	original := task.Fault()
	transport := &net.OpError{Op: "read", Net: "docker", Err: errors.New("connection lost")}
	observation := fmt.Errorf("list tasks for service api: %w", transport)
	progress := map[string]ServiceProgress{
		"api": {State: ServicePending, Message: "waiting for rolling update to complete", taskFault: original},
	}
	err := waitInterruptedError("app", progress, context.DeadlineExceeded, observation)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorIs(t, err, observation)
	assert.ErrorIs(t, err, transport)
	assert.ErrorIs(t, err, original)
	var coded faults.Coded
	require.ErrorAs(t, err, &coded)
	assert.Same(t, original, coded)
	assert.Equal(t, task.Error, coded.FaultSummary())
	var networkError *net.OpError
	require.ErrorAs(t, err, &networkError)
	assert.Same(t, transport, networkError)
	assert.ErrorContains(t, err, "wait for services in stack app")
	assert.ErrorContains(t, err, "list tasks for service api: read docker: connection lost")
	assert.ErrorContains(t, err, "waiting for rolling update to complete")
	assert.Equal(t, 1, strings.Count(err.Error(), task.Error))
}

func TestServiceWaiterPendingSchedulingFaultAtTimeout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		diagnostic string
		stale      bool
		otherSpec  bool
		shutdown   bool
		wantFault  bool
		initial    bool
	}{
		{name: "constraints on initial task", diagnostic: "no suitable node (scheduling constraints not satisfied on 1 node)", wantFault: true, initial: true},
		{name: "resources", diagnostic: "no suitable node (insufficient resources on 1 node)", wantFault: true},
		{name: "nodes unavailable", diagnostic: "no suitable node (1 node not available for new tasks)", wantFault: true},
		{name: "full diagnostic", diagnostic: "no suitable node (" + strings.Repeat("node not available; ", 30) + ")", wantFault: true},
		{name: "ordinary pending"},
		{name: "ambiguous pending", diagnostic: "waiting for assignment"},
		{name: "old task", diagnostic: "no suitable node", stale: true},
		{name: "different template", diagnostic: "no suitable node", otherSpec: true},
		{name: "retired task", diagnostic: "no suitable node", shutdown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			if !tc.initial {
				service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
			}
			task := testTask("pending-task", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
			task.Error = tc.diagnostic
			if tc.stale {
				task.CreatedAt = task.CreatedAt.Add(-time.Minute)
			}
			if tc.otherSpec {
				task.Spec = swarm.NewTaskSpec(dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "other:1"}})
			}
			if tc.shutdown {
				task.DesiredState = swarm.TaskStateShutdown
			}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil).AnyTimes()
			var updates []ServiceProgress
			result, err := NewServiceWaiter(manager, nil, time.Millisecond, 20*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"},
				Progress: func(p ServiceProgress) { updates = append(updates, p) },
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.NotErrorIs(t, err, ErrConvergenceFailed)
			assert.Equal(t, []string{"api"}, result.UnresolvedServices)
			assert.Empty(t, result.FailedTasks, "a pending scheduling error must not become a terminal task failure")
			require.Len(t, updates, 2, "unchanged pending progress must still be de-duplicated")
			assert.Equal(t, ServicePending, updates[0].State)
			assert.Equal(t, ServiceFailed, updates[1].State)
			var coded faults.Coded
			if !tc.wantFault {
				assert.NotErrorAs(t, err, &coded)
				return
			}
			require.ErrorAs(t, err, &coded)
			assert.Equal(t, faults.CodeNoSuitableNode, coded.FaultCode())
			assert.Equal(t, boundedDiagnostic(tc.diagnostic), coded.FaultSummary())
			assert.EqualError(t, coded, tc.diagnostic)
			assert.ErrorContains(t, err, tc.diagnostic)
			if !tc.initial {
				assert.ErrorContains(t, err, "waiting for rolling update to complete")
			}
		})
	}
}

func TestServiceWaiterClearsRecoveredSchedulingFault(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending without scheduler error", true: "converged"}[ready], func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			task := testTask("pending-task", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
			task.Error = "no suitable node (insufficient resources on 1 node)"
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			first := manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil)
			task.Error = ""
			if ready {
				task.CurrentState = swarm.TaskStateRunning
			}
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).After(first).Return([]swarm.ServiceTask{task}, nil).AnyTimes()
			err := waitForTest(NewServiceWaiter(manager, nil, time.Millisecond, 20*time.Millisecond), t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api"},
			})
			if ready {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				var coded faults.Coded
				assert.False(t, errors.As(err, &coded))
				assert.NotContains(t, err.Error(), "no suitable node")
			}
		})
	}
}
