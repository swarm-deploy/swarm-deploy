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

func TestServiceWaiterContainerdLeaseFindingOnRollback(t *testing.T) {
	const diagnostic = `failed to prepare extraction snapshot "extract-225092350-0LGf sha256:95dbfedee91178a3278e2e146a03bfd2223378ee9f60480b71df6f8d6cc1316e": NotFound: lease does not exist: not found`
	const summary = "Container image extraction failed: containerd snapshot lease is missing. Retry deployment; check for concurrent image cleanup."
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	service.UpdateStatus = &swarm.ServiceUpdateStatus{
		State: "rollback_started",
		Message: "update rolled back due to failure or early termination of task current-task",
		StartedAt: serviceWaitTestTime,
	}
	task := testTask("current-task", 1, swarm.TaskStateShutdown, swarm.TaskStateRejected, serviceWaitTestTime)
	task.Error = diagnostic
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil)
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{service.Name},
		ExpectedSpecs: map[string]dockerswarm.ServiceSpec{service.Name: service.ServiceSpec},
	})
	require.ErrorIs(t, err, ErrConvergenceFailed)
	assert.ErrorContains(t, err, diagnostic)
	var coded faults.Coded
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, faults.CodeImageExtractionFailed, coded.FaultCode())
	assert.Equal(t, summary, coded.FaultSummary())
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
	waiter := &ServiceWaiter{}
	err := waiter.waitInterruptedError(t.Context(), "app", progress, context.DeadlineExceeded, observation)
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
	const (
		dockerDiagnostic = "no suitable node (scheduling constraints not satisfied on 1 node)"
		regionConstraint = "node.labels.region == eu"
		hostConstraint   = "node.hostname == us-node"
	)
	nodeEU := swarm.Node{ID: "node-eu", Hostname: "eu-node", Labels: map[string]string{"region": "eu"}}
	nodeUS := swarm.Node{ID: "node-us", Hostname: "us-node", Labels: map[string]string{"region": "us"}}
	for _, tc := range []struct {
		name        string
		diagnostic  string
		constraints []string
		nodes       []swarm.Node
		nodeListErr error
		stale       bool
		otherSpec   bool
		shutdown    bool
		wantFault   bool
		initial     bool
		wantSummary string
	}{
		{
			name: "constraint on initial task", diagnostic: dockerDiagnostic, wantFault: true, initial: true,
			constraints: []string{regionConstraint}, nodes: []swarm.Node{nodeUS},
			wantSummary: "No nodes match placement constraint: " + regionConstraint,
		},
		{
			name: "no node matches all constraints", diagnostic: dockerDiagnostic, wantFault: true,
			constraints: []string{regionConstraint, hostConstraint}, nodes: []swarm.Node{nodeEU, nodeUS},
			wantSummary: "No nodes match placement constraints: " + regionConstraint + ", " + hostConstraint,
		},
		{
			name: "desired node role cannot confirm actual role", diagnostic: dockerDiagnostic, wantFault: true,
			constraints: []string{regionConstraint, " NoDe.RoLe != worker "},
		},
		{
			name: "matching node unavailable for scheduling", diagnostic: "no suitable node (1 node not available for new tasks)", wantFault: true,
			constraints: []string{regionConstraint}, nodes: []swarm.Node{{ID: "drained-eu", Availability: "drain", Labels: nodeEU.Labels}},
		},
		{
			name: "node list error", diagnostic: dockerDiagnostic, wantFault: true,
			constraints: []string{regionConstraint}, nodeListErr: errors.New("node list unavailable"),
		},
		{
			name: "empty node snapshot", diagnostic: dockerDiagnostic, wantFault: true,
			constraints: []string{regionConstraint}, nodes: []swarm.Node{},
		},
		{
			name: "invalid constraint", diagnostic: dockerDiagnostic, wantFault: true,
			constraints: []string{"node.labels.region = eu"},
		},
		{name: "resources", diagnostic: "no suitable node (insufficient resources on 1 node)", wantFault: true},
		{name: "nodes unavailable", diagnostic: "no suitable node (1 node not available for new tasks)", wantFault: true},
		{name: "full diagnostic", diagnostic: "no suitable node (" + strings.Repeat("node not available; ", 30) + ")", wantFault: true},
		{name: "ordinary pending"},
		{name: "ambiguous pending", diagnostic: "waiting for assignment"},
		{name: "old task", diagnostic: "no suitable node", constraints: []string{regionConstraint}, stale: true},
		{name: "different template", diagnostic: "no suitable node", otherSpec: true},
		{name: "retired task", diagnostic: "no suitable node", shutdown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			if tc.constraints != nil {
				service.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: tc.constraints}
			}
			if !tc.initial {
				service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "updating", StartedAt: serviceWaitTestTime}
			}
			task := testTask("pending-task", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
			task.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
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
			if tc.nodes != nil || tc.nodeListErr != nil {
				nodes.EXPECT().List(gomock.Any()).Return(tc.nodes, tc.nodeListErr)
			}
			var updates []ServiceProgress
			result, err := NewServiceWaiter(manager, nodes, time.Millisecond, 20*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
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
			if tc.wantSummary == "" {
				assert.Equal(t, boundedDiagnostic(tc.diagnostic), coded.FaultSummary())
			} else {
				assert.Equal(t, tc.wantSummary, coded.FaultSummary())
				var original faults.Coded
				require.ErrorAs(t, errors.Unwrap(coded), &original)
				assert.Equal(t, tc.diagnostic, original.FaultSummary())
			}
			assert.EqualError(t, coded, tc.diagnostic)
			assert.ErrorContains(t, err, tc.diagnostic)
			if !tc.initial {
				assert.ErrorContains(t, err, "waiting for rolling update to complete")
			}
		})
	}
}

func TestServiceWaiterDiagnosesPlacementAfterConvergenceEnds(t *testing.T) {
	const (
		regionConstraint = "node.labels.region == eu"
		webConstraint    = "node.labels.region == ca"
		dockerDiagnostic = "no suitable node (scheduling constraints not satisfied on 1 node)"
	)
	permanentErr := errors.New("web task list: permission denied")
	for _, tc := range []struct {
		name           string
		taskListErr    error
		webPending     bool
		waitCause      error
		wantUnresolved []string
		wantSummary    string
	}{
		{"slow node list", nil, false, context.DeadlineExceeded, []string{"api"}, dockerDiagnostic},
		{
			"another service fails observation temporarily", faults.NewSafeError("web task list unavailable", true, "", ""),
			false, context.DeadlineExceeded, []string{"api", "web"}, "No nodes match placement constraint: " + regionConstraint,
		},
		{
			"another service fails observation permanently", permanentErr, false, context.Canceled,
			[]string{"api", "web"}, "No nodes match placement constraint: " + regionConstraint,
		},
		{
			"later service has unmatched constraints", nil, true, context.DeadlineExceeded,
			[]string{"api", "web"}, "No nodes match placement constraint: " + webConstraint,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			api := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			api.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{regionConstraint}}
			web := testReplicatedService("web", "web-id", 1, serviceWaitTestTime, "web:2")
			pending := testTask("pending", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
			pending.Spec = swarm.NewTaskSpec(api.ServiceSpec.TaskTemplate)
			pending.Error = dockerDiagnostic
			webTask := testTask("web-task", 1, swarm.TaskStateRunning, swarm.TaskStateRunning, serviceWaitTestTime)
			if tc.webPending {
				pending.Error = "no suitable node (insufficient resources on 1 node)"
				web.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{webConstraint}}
				webTask.CurrentState = swarm.TaskStatePending
				webTask.Error = dockerDiagnostic
			}
			webTask.Spec = swarm.NewTaskSpec(web.ServiceSpec.TaskTemplate)

			var observationCtx context.Context
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, _ swarm.ListServicesFilter) {
				observationCtx = ctx
			}).Return([]swarm.StackService{api, web}, nil).AnyTimes()
			manager.EXPECT().ListTasks(gomock.Any(), api.ID).Return([]swarm.ServiceTask{pending}, nil).MinTimes(1)
			manager.EXPECT().ListTasks(gomock.Any(), web.ID).Return([]swarm.ServiceTask{webTask}, tc.taskListErr).MinTimes(1)
			nodes.EXPECT().List(gomock.Any()).DoAndReturn(func(lookupCtx context.Context) ([]swarm.Node, error) {
				require.NotNil(t, observationCtx)
				assert.ErrorIs(t, observationCtx.Err(), tc.waitCause)
				assert.NoError(t, lookupCtx.Err())
				deadline, ok := lookupCtx.Deadline()
				require.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), placementDiagnosticTimeout)
				if tc.taskListErr != nil {
					return []swarm.Node{{ID: "us-node", Labels: map[string]string{"region": "us"}}}, nil
				}
				if tc.webPending {
					return []swarm.Node{{ID: "eu-node", Labels: map[string]string{"region": "eu"}}}, nil
				}
				<-lookupCtx.Done()
				return nil, lookupCtx.Err()
			})

			result, err := NewServiceWaiter(manager, nodes, time.Millisecond, 30*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"api", "web"},
			})
			if tc.waitCause == context.Canceled {
				require.NotErrorIs(t, err, context.Canceled)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			assert.Equal(t, tc.wantUnresolved, result.UnresolvedServices)
			if tc.taskListErr != nil {
				assert.ErrorIs(t, err, tc.taskListErr)
			}
			var coded faults.Coded
			require.ErrorAs(t, err, &coded)
			assert.Equal(t, faults.CodeNoSuitableNode, coded.FaultCode())
			assert.Equal(t, tc.wantSummary, coded.FaultSummary())
			assert.EqualError(t, coded, dockerDiagnostic)
		})
	}
}

func TestServiceWaiterClearsRecoveredSchedulingFault(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending without scheduler error", true: "converged"}[ready], func(t *testing.T) {
			controller := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(controller)
			nodes := swarm.NewMockNodeManager(controller)
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			service.ServiceSpec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.labels.region == eu"}}
			task := testTask("pending-task", 1, swarm.TaskStateRunning, swarm.TaskStatePending, serviceWaitTestTime)
			task.Spec = swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
			task.Error = "no suitable node (insufficient resources on 1 node)"
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			first := manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{task}, nil)
			task.Error = ""
			if ready {
				task.CurrentState = swarm.TaskStateRunning
			}
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).After(first).Return([]swarm.ServiceTask{task}, nil).AnyTimes()
			err := waitForTest(NewServiceWaiter(manager, nodes, time.Millisecond, 20*time.Millisecond), t.Context(), ServiceWaitRequest{
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
