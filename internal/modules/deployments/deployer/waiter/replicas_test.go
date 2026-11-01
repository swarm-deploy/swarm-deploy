package waiter

import (
	"context"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func externalService(replicas uint64, when time.Time, image string) swarm.StackService {
	service := testReplicatedService("job", "job-id", replicas, when, image)
	service.ServiceSpec.Labels = map[string]string{labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false"}
	service.ServiceSpec.Mode.Replicated = &dockerswarm.ReplicatedService{Replicas: &replicas}
	return service
}

func TestExternalReplicaVerificationDoesNotRequireRunningTasks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replicas uint64
		tasks    []swarm.ServiceTask
	}{
		{name: "completed task", replicas: 1, tasks: []swarm.ServiceTask{testTask("completed", 1, swarm.TaskStateRunning, swarm.TaskStateComplete, serviceWaitTestTime)}},
		{name: "no running tasks", replicas: 1},
		{name: "zero replicas"},
		{name: "replicas changed by scheduler", replicas: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			before := externalService(0, serviceWaitTestTime.Add(-time.Hour), "job:1")
			desired := externalService(0, serviceWaitTestTime, "job:2")
			after := externalService(tc.replicas, serviceWaitTestTime, "job:2")
			after.ServiceSpec.TaskTemplate.ForceUpdate = 42
			after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(tc.tasks, nil)
			var progress []ServiceProgress
			result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"job"}, AppliedServices: []string{"job"},
				Baseline: []swarm.StackService{before}, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": desired.ServiceSpec},
				Progress: func(p ServiceProgress) { progress = append(progress, p) },
			})
			require.NoError(t, err)
			assert.Empty(t, result.UnresolvedServices)
			assert.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
			require.NotEmpty(t, progress)
			assert.True(t, progress[len(progress)-1].SpecificationOnly)
			assert.Contains(t, progress[len(progress)-1].Message, "task health not verified")
		})
	}
}

func TestExternalReplicaVerificationWaitsForExpectedSpecAndFreshRollout(t *testing.T) {
	for _, historical := range []string{"completed", "paused"} {
		t.Run(historical, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			before := externalService(1, serviceWaitTestTime.Add(-time.Hour), "job:1")
			before.UpdateStatus = &swarm.ServiceUpdateStatus{State: historical, StartedAt: before.UpdatedAt}
			after := externalService(1, serviceWaitTestTime, "job:2")
			after.UpdateStatus = before.UpdateStatus
			// A scheduler mutation is fresh metadata, but is not the requested image update.
			schedulerOnly := externalService(2, serviceWaitTestTime, "job:1")
			schedulerOnly.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			ready := after
			ready.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: serviceWaitTestTime}
			gomock.InOrder(
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{before}, nil),
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{schedulerOnly}, nil),
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil),
			)
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{ready}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(nil, nil)
			var progress []ServiceProgress
			result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"job"}, AppliedServices: []string{"job"}, Baseline: []swarm.StackService{before},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": ready.ServiceSpec},
				Progress:      func(p ServiceProgress) { progress = append(progress, p) },
			})
			require.NoError(t, err)
			assert.Empty(t, result.UnresolvedServices)
			require.Len(t, progress, 4)
			for _, p := range progress[:3] {
				assert.Equal(t, ServicePending, p.State)
			}
			assert.Equal(t, ServiceReady, progress[3].State)
		})
	}
}

func TestExternalReplicaTerminalFailureStillFailsVerification(t *testing.T) {
	for _, state := range []string{"paused", "failed", "rollback_started", "rollback_paused", "rollback_completed"} {
		t.Run(state, func(t *testing.T) {
			expected := externalService(0, serviceWaitTestTime, "job:2")
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			before := externalService(0, serviceWaitTestTime.Add(-time.Hour), "job:1")
			after := externalService(0, serviceWaitTestTime, "job:2")
			if state == "rollback_completed" {
				after.ServiceSpec = before.ServiceSpec
			}
			after.UpdateStatus = &swarm.ServiceUpdateStatus{State: state, StartedAt: serviceWaitTestTime, Message: "update failed"}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(nil, nil)
			result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"job"}, AppliedServices: []string{"job"}, Baseline: []swarm.StackService{before},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": expected.ServiceSpec},
			})
			require.ErrorIs(t, err, ErrConvergenceFailed)
			assert.Equal(t, []string{"job"}, result.UnresolvedServices)
			assert.Empty(t, result.SpecificationOnlyServices)
		})
	}
}

func TestExternalReplicaUnresolvedCannotSucceedWithStaleSpecOrMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, image   string
		staleMetadata bool
	}{
		{name: "unapplied spec with fresh scheduler metadata", image: "job:1"},
		{name: "stale metadata", image: "job:2", staleMetadata: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			baseline := externalService(1, serviceWaitTestTime, "job:2")
			observedAt := serviceWaitTestTime.Add(time.Minute)
			if tc.staleMetadata {
				observedAt = serviceWaitTestTime.Add(-time.Hour)
			}
			observed := externalService(1, observedAt, tc.image)
			observed.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: observedAt}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{observed}, nil).AnyTimes()
			if tc.staleMetadata {
				manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(testRunningTasks(1, observedAt), nil).AnyTimes()
			}
			result, err := NewServiceWaiter(manager, time.Millisecond, 10*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
				StackName: "app", ServiceNames: []string{"job"}, UnresolvedServices: []string{"job"}, Baseline: []swarm.StackService{baseline},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": baseline.ServiceSpec},
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Equal(t, []string{"job"}, result.UnresolvedServices)
			assert.Empty(t, result.SpecificationOnlyServices)
		})
	}
}

func TestOrdinaryReplicasRetainStrictTaskVerificationWithExpectedSpec(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("job", "job-id", 1, serviceWaitTestTime, "job:2")
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return([]swarm.ServiceTask{
		testTask("completed", 1, swarm.TaskStateRunning, swarm.TaskStateComplete, serviceWaitTestTime),
	}, nil).AnyTimes()
	result, err := NewServiceWaiter(manager, time.Millisecond, 10*time.Millisecond).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"job"}, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": service.ServiceSpec},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, []string{"job"}, result.UnresolvedServices)
	assert.Empty(t, result.SpecificationOnlyServices)
}
