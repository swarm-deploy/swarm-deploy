package waiter

import (
	"context"
	"strings"
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
			w := NewServiceWaiter(manager, nil, time.Millisecond, time.Second)
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

func TestJobRegistryFailureAtDeadline(t *testing.T) {
	for _, mode := range []string{replicatedJobServiceMode, globalJobServiceMode} {
		for _, tc := range []struct {
			name        string
			iteration   uint64
			replacement swarm.TaskState
			wantCause   bool
		}{
			{"current rejection", 7, "", true},
			{"previous iteration", 6, "", false},
			{"replacement still preparing", 7, swarm.TaskStatePreparing, true},
			{"replacement prepared", 7, swarm.TaskStateReady, false},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					service := swarm.StackService{ID: "job-id", Name: "job", Mode: mode, DesiredTasks: 1, CompletedTasks: 1,
						JobStatus: &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}}
					if mode == replicatedJobServiceMode {
						service.ServiceSpec.Mode.ReplicatedJob = &dockerswarm.ReplicatedJob{TotalCompletions: new(uint64(2))}
					} else {
						service.ServiceSpec.Mode.GlobalJob = &dockerswarm.GlobalJob{}
					}
					service.ServiceSpec.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "job:1"}
					service.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionOnFailure}

					failed := testTask("failed", 0, swarm.TaskStateComplete, swarm.TaskStateRejected, time.Now())
					failed.Node, failed.Error = "node-a", "error from registry: unavailable"
					failed.JobIteration, failed.Spec = &dockerswarm.Version{Index: tc.iteration}, swarm.NewTaskSpec(service.ServiceSpec.TaskTemplate)
					// Completion on a different slot or node cannot hide this failure.
					completed := failed
					completed.ID, completed.Slot, completed.Node = "completed", 1, "node-b"
					completed.JobIteration = &service.JobStatus.JobIteration
					completed.CurrentState, completed.Error = swarm.TaskStateComplete, ""
					tasks := []swarm.ServiceTask{failed, completed}
					if tc.replacement != "" {
						tasks[0].DesiredState = swarm.TaskStateShutdown
						replacement := failed
						replacement.ID, replacement.Error = "replacement", ""
						replacement.CreatedAt = failed.CreatedAt.Add(time.Second)
						replacement.DesiredState, replacement.CurrentState = swarm.TaskStateReady, tc.replacement
						tasks = append(tasks, replacement)
						if mode == globalJobServiceMode {
							// Swarm omits the replacement in restart delay from DesiredTasks.
							service.DesiredTasks = 0
						}
					}
					manager := swarm.NewMockServiceManager(gomock.NewController(t))
					manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
					manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return(tasks, nil).AnyTimes()
					_, err := NewServiceWaiter(manager, nil, time.Second, 3*time.Second).Wait(t.Context(), ServiceWaitRequest{
						StackName: "app", ServiceNames: []string{service.Name},
					})
					require.ErrorIs(t, err, context.DeadlineExceeded)
					assert.NotErrorIs(t, err, ErrConvergenceFailed)
					causes := faults.Causes(err)
					if tc.wantCause {
						require.Len(t, causes, 1)
						assert.Equal(t, faults.CodeImagePullFailed, causes[0].Code)
						assert.True(t, causes[0].Temporary)
					} else {
						assert.Empty(t, causes)
					}
				})
			})
		}
	}
}
