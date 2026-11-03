package waiter

import (
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestJobFailureIsObservedWithoutWaitingForDeadline(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		t.Run(mode, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := swarm.StackService{ID: "job-id", Name: "job", Mode: mode, DesiredTasks: 1, JobStatus: &dockerswarm.JobStatus{}}
			if mode == "replicated-job" {
				service.ServiceSpec.Mode.ReplicatedJob = &dockerswarm.ReplicatedJob{}
			} else {
				service.ServiceSpec.Mode.GlobalJob = &dockerswarm.GlobalJob{}
				service.DesiredTasks = 0
			}
			service.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return([]swarm.ServiceTask{{
				JobIteration: &dockerswarm.Version{}, ID: "failed-job-task", Node: "node", CurrentState: swarm.TaskStateFailed, ExitCode: new(23),
				Error: "token=private-value " + strings.Repeat("x", 300),
			}}, nil)
			w := NewServiceWaiter(manager, time.Millisecond, time.Second)
			var observed ServiceProgress
			_, err := w.Wait(t.Context(), ServiceWaitRequest{StackName: "app", ServiceNames: []string{"job"},
				Progress: func(p ServiceProgress) { observed = p },
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.Contains(t, err.Error(), "failed-job-task")
			assert.Contains(t, err.Error(), "exit_code=23")
			assert.NotContains(t, err.Error(), "private-value")
			assert.LessOrEqual(t, len(observed.Message), maxServiceDiagnosticLength+3)
		})
	}
}

func TestJobProgressWaitsForRetryIteration(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		t.Run(mode, func(t *testing.T) {
			baseline := swarm.StackService{ID: "job", Mode: mode, JobStatus: &dockerswarm.JobStatus{}, CompletedTasks: 1}
			current := baseline
			current.ServiceSpec.TaskTemplate.ForceUpdate = 1
			progress := jobProgress(ServiceProgress{State: ServicePending}, baseline, current, nil)
			assert.Equal(t, ServicePending, progress.State, "old iteration cannot satisfy a retry")
			current.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 1}}
			progress = jobProgress(ServiceProgress{State: ServicePending}, baseline, current, nil)
			assert.Equal(t, ServiceReady, progress.State)
		})
	}
}
