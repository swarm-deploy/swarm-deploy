package services

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestNonRestartingTaskFailure(t *testing.T) {
	now := time.Now()
	exit := 23
	failed := swarm.ServiceTask{ID: "failed", Slot: 1, CurrentState: swarm.TaskStateFailed,
		CreatedAt: now, UpdatedAt: now, ExitCode: &exit}
	replacement := swarm.ServiceTask{ID: "replacement", Slot: 1, CurrentState: swarm.TaskStatePending,
		CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	for _, tc := range []struct {
		name      string
		condition dockerswarm.RestartPolicyCondition
		tasks     []swarm.ServiceTask
		since     time.Time
		replicas  uint64
		want      bool
	}{
		{name: "terminal current task", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}, replicas: 1, want: true},
		{name: "automatic replacement allowed", condition: dockerswarm.RestartPolicyConditionAny, tasks: []swarm.ServiceTask{failed}, replicas: 1},
		{name: "failure restart allowed", condition: dockerswarm.RestartPolicyConditionOnFailure, tasks: []swarm.ServiceTask{failed}, replicas: 1},
		{name: "new generation pending", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}, since: now.Add(time.Second), replicas: 1},
		{name: "replacement supersedes failure", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{replacement, failed}, replicas: 1},
		{name: "scaled down slot ignored", condition: dockerswarm.RestartPolicyConditionNone, tasks: []swarm.ServiceTask{failed}},
		{name: "no tasks yet", condition: dockerswarm.RestartPolicyConditionNone, replicas: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := serviceSpec("api", "busybox:1.36")
			spec.Mode.Replicated.Replicas = &tc.replicas
			spec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: tc.condition}
			actual, found := NonRestartingTaskFailure(spec, tc.tasks, tc.since)
			assert.Equal(t, tc.want, found)
			if found {
				assert.Equal(t, failed.ID, actual.ID)
			}
		})
	}
}

func TestApplyExplicitRetryOfInitialFailure(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged failure is not forced", true: "explicit retry forces new generation"}[retry], func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			desired := serviceSpec("api", "busybox:1.36")
			desired.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
			current := service("api-id", 7, desired)
			current.UpdatedAt = time.Now()
			manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(current, nil)
			if retry {
				exit := 23
				manager.EXPECT().ListTasks(gomock.Any(), current.ID).Return([]swarm.ServiceTask{{
					ID: "initial-failed", Slot: 1, CurrentState: swarm.TaskStateFailed, ExitCode: &exit,
					CreatedAt: current.UpdatedAt.Add(time.Second),
				}}, nil).Times(2)
				forced := cloneSpec(desired)
				forced.TaskTemplate.ForceUpdate++
				manager.EXPECT().Update(gomock.Any(), current.ID, current.Version, forced, gomock.Any()).Return(nil)
			}
			result, err := New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "demo", []dockerswarm.ServiceSpec{desired}, ApplyOptions{RetryFailedUpdates: retry})
			require.NoError(t, err)
			if retry {
				assert.Equal(t, []string{"api"}, result.ChangedServices)
			} else {
				assert.Empty(t, result.ChangedServices)
			}
		})
	}
}
