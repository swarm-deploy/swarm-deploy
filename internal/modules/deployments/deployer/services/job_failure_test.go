package services

import (
	"strconv"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func jobSpec(mode string) dockerswarm.ServiceSpec {
	spec := serviceSpec("job", "busybox:1.36")
	spec.Mode = dockerswarm.ServiceMode{}
	if mode == "replicated-job" {
		spec.Mode.ReplicatedJob = &dockerswarm.ReplicatedJob{}
	} else {
		spec.Mode.GlobalJob = &dockerswarm.GlobalJob{}
	}
	spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
	return spec
}

func jobTask(id string, state swarm.TaskState, iteration uint64, created time.Time) swarm.ServiceTask {
	return swarm.ServiceTask{ID: id, Node: "node-a", Slot: 0, CurrentState: state,
		JobIteration: &dockerswarm.Version{Index: iteration}, CreatedAt: created, UpdatedAt: created}
}

func TestJobTaskFailureUsesIterationAndReplacementEvidence(t *testing.T) {
	now := time.Now()
	failed := jobTask("failed", swarm.TaskStateFailed, 7, now)
	failed.ExitCode = new(23)
	old := failed
	old.JobIteration = &dockerswarm.Version{Index: 6}
	missing := failed
	missing.JobIteration = nil
	future := failed
	future.JobIteration = &dockerswarm.Version{Index: 8}
	zero := failed
	zero.ExitCode = new(0)
	pending := jobTask("pending", swarm.TaskStatePending, 7, now.Add(time.Second))
	complete := jobTask("complete", swarm.TaskStateComplete, 7, now.Add(time.Second))
	sameTime := pending
	sameTime.CreatedAt = now
	unrelated := pending
	unrelated.Slot, unrelated.Node = 1, "node-b"
	for _, mode := range []string{"replicated-job", "global-job"} {
		for _, tc := range []struct {
			name   string
			tasks  []swarm.ServiceTask
			failed bool
		}{
			{"current failed task", []swarm.ServiceTask{failed}, true},
			{"historical failed task", []swarm.ServiceTask{old}, false},
			{"future task", []swarm.ServiceTask{future}, false},
			{"missing iteration", []swarm.ServiceTask{missing}, false},
			{"known zero exit", []swarm.ServiceTask{zero}, false},
			{"scheduler not initialized", nil, false},
			{"pending replacement", []swarm.ServiceTask{pending, failed}, false},
			{"completed replacement", []swarm.ServiceTask{failed, complete}, false},
			{"completed current iteration with historical failure", []swarm.ServiceTask{old, complete}, false},
			{"equal time pending first", []swarm.ServiceTask{sameTime, failed}, false},
			{"equal time pending last", []swarm.ServiceTask{failed, sameTime}, false},
			{"independent pending task cannot hide failure", []swarm.ServiceTask{failed, unrelated}, true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				spec := jobSpec(mode)
				_, found := JobTaskFailure(spec, &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}, tc.tasks)
				assert.Equal(t, tc.failed, found)
				_, found = JobTaskFailure(spec, nil, tc.tasks)
				assert.False(t, found)
				spec.TaskTemplate.RestartPolicy.Condition = dockerswarm.RestartPolicyConditionOnFailure
				_, found = JobTaskFailure(spec, &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}, tc.tasks)
				assert.False(t, found, "automatic retries remain pending")
			})
		}
		for _, state := range []swarm.TaskState{swarm.TaskStateRejected, swarm.TaskStateOrphaned} {
			t.Run(mode+"/"+string(state), func(t *testing.T) {
				_, found := JobTaskFailure(jobSpec(mode), &dockerswarm.JobStatus{}, []swarm.ServiceTask{jobTask("terminal", state, 0, now)})
				assert.True(t, found)
			})
		}
	}
	spec := jobSpec("replicated-job")
	spec.Mode.ReplicatedJob.TotalCompletions = new(uint64(0))
	_, found := JobTaskFailure(spec, &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}, []swarm.ServiceTask{failed})
	assert.False(t, found, "zero completions never fail from historical slots")
}

func TestApplyJobRetryOnlyForCurrentUnreplacedFailure(t *testing.T) {
	now := time.Now()
	for _, mode := range []string{"replicated-job", "global-job"} {
		for _, tc := range []struct {
			name      string
			retry     bool
			iteration uint64
			state     swarm.TaskState
			update    bool
		}{
			{"ordinary sync", false, 7, swarm.TaskStateFailed, false},
			{"explicit failed retry", true, 7, swarm.TaskStateFailed, true},
			{"historical failure", true, 6, swarm.TaskStateFailed, false},
			{"completed job", true, 7, swarm.TaskStateComplete, false},
			{"pending execution", true, 7, swarm.TaskStatePending, false},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				desired := jobSpec(mode)
				current := service("job-id", 4, cloneSpec(desired))
				current.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}
				current.Spec.TaskTemplate.ForceUpdate = 9
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
				if tc.retry {
					calls := 1
					if tc.update {
						calls++
					}
					manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{jobTask("task", tc.state, tc.iteration, now)}, nil).Times(calls)
				}
				if tc.update {
					manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
							assert.Equal(t, uint64(10), spec.TaskTemplate.ForceUpdate)
							return nil
						})
				}
				result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: tc.retry})
				require.NoError(t, err)
				assert.Equal(t, tc.update, len(result.ChangedServices) != 0)
				assert.Zero(t, desired.TaskTemplate.ForceUpdate)
			})
		}
	}
}

func TestJobRetryRechecksIterationAfterVersionConflict(t *testing.T) {
	for _, mode := range []string{"replicated-job", "global-job"} {
		for _, advanced := range []bool{false, true} {
			t.Run(mode+"/advanced="+strconv.FormatBool(advanced), func(t *testing.T) {
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				desired := jobSpec(mode)
				current := service("job-id", 4, cloneSpec(desired))
				current.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}
				current.Spec.TaskTemplate.ForceUpdate = 9
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
				failed := jobTask("failed", swarm.TaskStateFailed, 7, time.Now())
				reads := manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{failed}, nil).Times(2)
				conflict := manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).After(reads).DoAndReturn(
					func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
						assert.Equal(t, uint64(10), spec.TaskTemplate.ForceUpdate)
						return swarm.ErrServiceConflict
					})
				newer := current
				newer.Version.Index++
				newer.Spec = cloneSpec(current.Spec)
				newer.Spec.TaskTemplate.ForceUpdate = 10
				newer.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 8}}
				tasks := []swarm.ServiceTask{failed}
				calls := 2
				if advanced {
					tasks = append(tasks, jobTask("replacement", swarm.TaskStatePending, 8, time.Now()))
					calls = 1
				} else {
					tasks = append(tasks, jobTask("concurrent-failed", swarm.TaskStateFailed, 8, time.Now()))
				}
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).After(conflict).Return(newer, nil)
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).After(conflict).Return(tasks, nil).Times(calls)
				if !advanced {
					manager.EXPECT().Update(gomock.Any(), current.ID, newer.Version, gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
							assert.Equal(t, uint64(11), spec.TaskTemplate.ForceUpdate)
							return nil
						})
				}
				result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: true})
				require.NoError(t, err)
				assert.Equal(t, []string{"job"}, result.ChangedServices)
			})
		}
	}
}

func TestApplyJobManualRetryAfterMaxAttempts(t *testing.T) {
	now := time.Now()
	for _, condition := range []dockerswarm.RestartPolicyCondition{
		dockerswarm.RestartPolicyConditionOnFailure, dockerswarm.RestartPolicyConditionAny, "",
	} {
		for _, tc := range []struct {
			name   string
			retry  bool
			states []swarm.TaskState
			update bool
		}{
			{"exhausted", true, []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStateFailed}, true},
			{"automatic attempt remains", true, []swarm.TaskState{swarm.TaskStateFailed}, false},
			{"replacement pending", true, []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStatePending}, false},
			{"completed", true, []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStateComplete}, false},
			{"manual retry disabled", false, []swarm.TaskState{swarm.TaskStateFailed, swarm.TaskStateFailed}, false},
		} {
			t.Run("condition="+string(condition)+"/"+tc.name, func(t *testing.T) {
				manager := swarm.NewMockServiceManager(gomock.NewController(t))
				desired := jobSpec("replicated-job")
				desired.TaskTemplate.RestartPolicy.Condition = condition
				desired.TaskTemplate.RestartPolicy.MaxAttempts = new(uint64(1))
				current := service("job-id", 4, cloneSpec(desired))
				current.JobStatus = &dockerswarm.JobStatus{JobIteration: dockerswarm.Version{Index: 7}}
				current.Spec.TaskTemplate.ForceUpdate = 9
				manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
				var tasks []swarm.ServiceTask
				for i, state := range tc.states {
					tasks = append(tasks, jobTask(strconv.Itoa(i), state, 7, now.Add(time.Duration(i)*time.Second)))
				}
				if tc.retry {
					calls := 1
					if tc.update {
						calls++
					}
					manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return(tasks, nil).Times(calls)
				}
				if tc.update {
					manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
							assert.Equal(t, uint64(10), spec.TaskTemplate.ForceUpdate)
							return nil
						})
				}
				result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: tc.retry})
				require.NoError(t, err)
				assert.Equal(t, tc.update, len(result.ChangedServices) > 0)
			})
		}
	}
}
