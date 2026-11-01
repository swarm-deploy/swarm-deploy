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

func TestPausedUpdateIncludesLatestFailedTask(t *testing.T) {
	for _, state := range []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateOrphaned} {
		t.Run(string(state), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
			service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", Message: "update paused due to early termination"}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			exit := 137
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return([]swarm.ServiceTask{
				{ID: "old-task", CurrentState: swarm.TaskStateFailed, UpdatedAt: serviceWaitTestTime.Add(-time.Minute), Error: "old failure"},
				{ID: "latest-task", CurrentState: state, UpdatedAt: serviceWaitTestTime, Error: "container startup failed", ExitCode: &exit},
			}, nil)
			err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "demo", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{service},
			})
			require.Error(t, err)
			for _, detail := range []string{"api", "paused", "latest-task", "state=" + string(state), "exit_code=137", "container startup failed"} {
				assert.ErrorContains(t, err, detail)
			}
			assert.NotContains(t, err.Error(), "old failure")
		})
	}
}

func TestPausedUpdateRemainsFailedWhenTaskDiagnosticsUnavailable(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
	service.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", Message: "original failure"}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "api-id").Return(nil, errors.New("temporary observation failure"))
	err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{StackName: "demo", ServiceNames: []string{"api"}})
	require.ErrorContains(t, err, "original failure")
	assert.ErrorContains(t, err, "task diagnostics unavailable")
}

func TestTerminalDiagnosticUsesTaskNamedByDockerIncludingEarlySuccessfulExit(t *testing.T) {
	exit := 0
	diagnostic := terminalTaskDiagnostic([]swarm.ServiceTask{
		{ID: "unrelated", CurrentState: swarm.TaskStateFailed, UpdatedAt: serviceWaitTestTime},
		{ID: "early-exit", CurrentState: swarm.TaskStateComplete, ExitCode: &exit, UpdatedAt: serviceWaitTestTime.Add(-time.Minute)},
	}, "update paused due to early termination of task early-exit")
	assert.Contains(t, diagnostic, "id=early-exit state=complete exit_code=0")
	assert.NotContains(t, diagnostic, "unrelated")
}
