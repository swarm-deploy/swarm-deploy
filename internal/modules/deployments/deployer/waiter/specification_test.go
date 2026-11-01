package waiter

import (
	"context"
	"fmt"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestExternalExecutionStatusAttribution(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                                  string
		state                                                                                                 string
		external, unchanged, unresolved, missingSpec, staleMetadata, staleStatus, futureStatus, missingStatus bool
		want                                                                                                  ServiceState
	}{
		{name: "accepted spec then external paused execution", state: "paused", external: true, want: ServiceReady},
		{name: "accepted spec then external failed execution", state: "failed", external: true, want: ServiceReady},
		{name: "external execution still updating", state: "updating", external: true, want: ServiceReady},
		{name: "external rollback loses expected spec", state: "rollback_completed", external: true, missingSpec: true, want: ServicePending},
		{name: "historical paused unrelated to unchanged spec", state: "paused", unchanged: true, want: ServiceReady},
		{name: "current deployment genuinely paused", state: "paused", want: ServiceFailed},
		{name: "current deployment genuinely failed", state: "failed", want: ServiceFailed},
		{name: "current deployment rolled back", state: "rollback_completed", missingSpec: true, want: ServiceFailed},
		{name: "external paused execution without expected spec", state: "paused", external: true, missingSpec: true, want: ServicePending},
		{name: "external completed execution without expected spec", state: "completed", external: true, missingSpec: true, want: ServicePending},
		{name: "stale metadata after actual apply inspection", state: "paused", staleMetadata: true, external: true, want: ServicePending},
		{name: "historical paused cannot describe new write", state: "paused", staleStatus: true, want: ServicePending},
		{name: "historical completed cannot describe new write", state: "completed", staleStatus: true, want: ServicePending},
		{name: "status newer than snapshot", state: "paused", futureStatus: true, want: ServicePending},
		{name: "current update still pending", state: "updating", want: ServicePending},
		{name: "inherited failed obligation remains failed", state: "paused", unchanged: true, unresolved: true, want: ServiceFailed},
		{name: "inherited obligation verifies successful recovery", state: "completed", unchanged: true, unresolved: true, want: ServiceReady},
		{name: "inherited obligation cannot pass missing status", unchanged: true, unresolved: true, missingStatus: true, want: ServicePending},
		{name: "inherited failed obligation gets external generation", state: "paused", external: true, unchanged: true, unresolved: true, want: ServiceReady},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := externalService(0, serviceWaitTestTime.Add(-time.Hour), "job:1")
			// The scheduler already advanced from 7 to 9 between stack snapshot and actual apply.
			before.ServiceSpec.TaskTemplate.ForceUpdate = 7
			expected := externalService(0, serviceWaitTestTime, "job:2")
			after := externalService(3, serviceWaitTestTime, "job:2")
			after.ServiceSpec.TaskTemplate.ForceUpdate = 9
			if tc.external {
				after.ServiceSpec.TaskTemplate.ForceUpdate = 10
			}
			if tc.unchanged {
				before = externalService(0, serviceWaitTestTime.Add(-time.Hour), "job:2")
				before.ServiceSpec.TaskTemplate.ForceUpdate = 9
				before.UpdateStatus = &swarm.ServiceUpdateStatus{State: "paused", StartedAt: before.UpdatedAt}
			}
			if tc.missingSpec {
				after.ServiceSpec.TaskTemplate.ContainerSpec.Image = "job:1"
			}
			after.CreatedAt = serviceWaitTestTime.Add(-2 * time.Hour)
			after.UpdateStatus = &swarm.ServiceUpdateStatus{State: tc.state, StartedAt: serviceWaitTestTime}
			if tc.staleMetadata {
				after.UpdatedAt = serviceWaitTestTime.Add(-45 * time.Minute)
			}
			if tc.staleStatus {
				after.UpdateStatus.StartedAt = before.UpdatedAt
			}
			if tc.futureStatus {
				after.UpdateStatus.StartedAt = serviceWaitTestTime.Add(time.Minute)
			}
			if tc.missingStatus {
				after.UpdateStatus = nil
			}
			request := ServiceWaitRequest{StackName: "app", ServiceNames: []string{"job"}, Baseline: []swarm.StackService{before},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": expected.ServiceSpec}}
			if tc.unresolved {
				request.UnresolvedServices = []string{"job"}
			}
			if !tc.unchanged {
				request.AppliedServices = []string{"job"}
				request.ExternalUpdates = map[string]services.UpdateGeneration{"job": {
					ServiceID: "job-id", ForceUpdate: 9, After: serviceWaitTestTime.Add(-30 * time.Minute),
				}}
			}
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{after}, nil).AnyTimes()
			if tc.want != ServicePending {
				manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return([]swarm.ServiceTask{
					testTask("completed-job", 1, swarm.TaskStateRunning, swarm.TaskStateComplete, serviceWaitTestTime),
				}, nil)
			}
			result, err := NewServiceWaiter(manager, time.Millisecond, 15*time.Millisecond).Wait(t.Context(), request)
			switch tc.want {
			case ServiceReady:
				require.NoError(t, err)
				assert.Empty(t, result.UnresolvedServices)
				assert.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
			case ServiceFailed:
				require.ErrorIs(t, err, ErrConvergenceFailed)
				assert.Equal(t, []string{"job"}, result.UnresolvedServices)
				assert.Empty(t, result.SpecificationOnlyServices)
			case ServicePending:
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.NotErrorIs(t, err, ErrConvergenceFailed)
				assert.Equal(t, []string{"job"}, result.UnresolvedServices)
				assert.Empty(t, result.SpecificationOnlyServices)
			default:
				t.Fatal(fmt.Sprint(tc.want))
			}
		})
	}
}

func TestWaiterRejectsExternalOwnershipOnGlobalService(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := externalService(0, serviceWaitTestTime, "job:1")
	service.Mode = "global"
	service.ServiceSpec.Mode = dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}
	manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
	result, err := newTestServiceWaiter(manager).Wait(t.Context(), ServiceWaitRequest{
		StackName: "app", ServiceNames: []string{"job"},
		ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": service.ServiceSpec},
	})
	require.ErrorContains(t, err, "only for replicated services")
	assert.Equal(t, []string{"job"}, result.UnresolvedServices)
	assert.Empty(t, result.SpecificationOnlyServices)
}
