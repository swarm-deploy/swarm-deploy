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
				{ID: "latest-task", CurrentState: state, CreatedAt: serviceWaitTestTime, UpdatedAt: serviceWaitTestTime, Error: "container startup failed", ExitCode: &exit},
			}, nil)
			err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{
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
	err := waitForTest(newTestServiceWaiter(manager), t.Context(), ServiceWaitRequest{StackName: "demo", ServiceNames: []string{"api"}})
	require.ErrorContains(t, err, "original failure")
	assert.ErrorContains(t, err, "task diagnostics unavailable")
}

func TestTerminalDiagnosticUsesTaskNamedByDockerIncludingEarlySuccessfulExit(t *testing.T) {
	exit := 0
	diagnostic := formatTaskDiagnostic(terminalFailureTask([]swarm.ServiceTask{
		{ID: "unrelated", CurrentState: swarm.TaskStateFailed, UpdatedAt: serviceWaitTestTime},
		{ID: "early-exit", CurrentState: swarm.TaskStateComplete, ExitCode: &exit, UpdatedAt: serviceWaitTestTime.Add(-time.Minute)},
	}, "update paused due to early termination of task early-exit", serviceWaitTestTime))
	assert.Contains(t, diagnostic, "id=early-exit state=complete exit_code=0")
	assert.NotContains(t, diagnostic, "unrelated")
}

func TestTerminalFailureTaskSelectionExcludesHistoricalFailures(t *testing.T) {
	now := serviceWaitTestTime
	current := swarm.ServiceTask{ID: "new-task", CurrentState: swarm.TaskStateFailed, CreatedAt: now, UpdatedAt: now}
	historical := swarm.ServiceTask{ID: "historical-task", CurrentState: swarm.TaskStateFailed,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(time.Hour)}
	for _, tc := range []struct {
		name, message string
		tasks         []swarm.ServiceTask
		want          string
	}{
		{name: "current failure", tasks: []swarm.ServiceTask{historical, current}, want: current.ID},
		{name: "historical failure only", tasks: []swarm.ServiceTask{historical}},
		{name: "no tasks"},
		{name: "Docker identifies older task", tasks: []swarm.ServiceTask{current, historical},
			message: "failure of task historical-task", want: historical.ID},
		{name: "ID must match completely", tasks: []swarm.ServiceTask{historical}, message: "failure of task historical-task-longer"},
		{name: "quoted Docker ID", tasks: []swarm.ServiceTask{historical}, message: "failure of task \"historical-task\"", want: historical.ID},
		{name: "healthy task never a fallback", tasks: []swarm.ServiceTask{{ID: "healthy", CurrentState: swarm.TaskStateRunning, CreatedAt: now}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, terminalFailureTask(tc.tasks, tc.message, now).ID)
		})
	}
}

func TestTerminalTaskFallbackUsesRolloutBoundary(t *testing.T) {
	now := serviceWaitTestTime
	for _, tc := range []struct {
		name     string
		baseline swarm.StackService
		service  swarm.StackService
		want     time.Time
	}{
		{name: "update start", service: swarm.StackService{UpdatedAt: now.Add(time.Minute),
			UpdateStatus: &swarm.ServiceUpdateStatus{StartedAt: now}}, want: now},
		{name: "missing update start uses metadata", service: swarm.StackService{UpdatedAt: now,
			UpdateStatus: &swarm.ServiceUpdateStatus{}}, want: now},
		{name: "pre-apply boundary", baseline: swarm.StackService{ID: "api", UpdatedAt: now},
			service: swarm.StackService{ID: "api", UpdatedAt: now.Add(time.Minute), UpdateStatus: &swarm.ServiceUpdateStatus{}}, want: now},
		{name: "rollback starts after failed new task", baseline: swarm.StackService{ID: "api", UpdatedAt: now},
			service: swarm.StackService{ID: "api", UpdatedAt: now.Add(time.Minute),
				UpdateStatus: &swarm.ServiceUpdateStatus{State: "rollback_started", StartedAt: now.Add(time.Minute)}}, want: now},
		{name: "creation boundary", service: swarm.StackService{CreatedAt: now, UpdateStatus: &swarm.ServiceUpdateStatus{}}, want: now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, terminalTaskCutoff(tc.baseline, tc.service))
		})
	}
	assert.Empty(t, terminalFailureTask([]swarm.ServiceTask{{ID: "unknown-generation", CurrentState: swarm.TaskStateFailed}}, "", time.Time{}).ID)
}
