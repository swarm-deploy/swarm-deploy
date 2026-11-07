package waiter

import (
	"context"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRollbackStatusBelongsToDesiredDeployment(t *testing.T) {
	for _, tc := range []struct {
		name, status                          string
		desiredB, currentRollback, unresolved bool
		running                               int
		want                                  ServiceState
	}{
		{name: "healthy A", running: 1, want: ServiceReady},
		{name: "failed B rolled back to A", status: "rollback_completed", desiredB: true, running: 1, want: ServiceFailed},
		{name: "Git revert to A", status: "rollback_completed", running: 1, want: ServiceReady},
		{name: "Git revert clears unresolved B", status: "rollback_completed", unresolved: true, running: 1, want: ServiceReady},
		{name: "restored A still needs replicas", status: "rollback_completed", unresolved: true, want: ServicePending},
		{name: "current generation rollback", status: "rollback_completed", currentRollback: true, running: 1, want: ServiceFailed},
		{name: "current generation B rollback", status: "rollback_completed", desiredB: true, currentRollback: true, running: 1, want: ServiceFailed},
		{name: "paused A", status: "paused", running: 1, want: ServiceFailed},
		{name: "rollback in progress", status: "rollback_started", running: 1, want: ServiceFailed},
		{name: "rollback failed", status: "rollback_paused", running: 1, want: ServiceFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:A")
			service.ServiceSpec.Labels = map[string]string{labelsdict.StackNamespace: "app"}
			service.UpdatedAt = serviceWaitTestTime.Add(time.Minute)
			if tc.status != "" {
				service.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateState(tc.status), StartedAt: serviceWaitTestTime.Add(time.Second)}
			}
			baseline := service
			if tc.currentRollback {
				baseline.UpdatedAt = serviceWaitTestTime
				baseline.UpdateStatus = nil
			}
			expected := service.ServiceSpec
			if tc.desiredB {
				container := *expected.TaskTemplate.ContainerSpec
				container.Image = "api:B"
				expected.TaskTemplate.ContainerSpec = &container
			}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
			// Running A tasks may predate the failed B rollout.
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return(testRunningTasks(tc.running, serviceWaitTestTime, service.ServiceSpec.TaskTemplate), nil).AnyTimes()
			request := ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{baseline},
				ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"api": expected}}
			if tc.unresolved {
				request.UnresolvedServices = []string{"api"}
			}
			if tc.currentRollback {
				request.AppliedServices = []string{"api"}
			}
			if tc.want == ServiceReady && tc.status != "" {
				// No Update expectation: the applier must skip the restored desired spec.
				manager.EXPECT().Inspect(gomock.Any(), expected.Name).Return(dockerswarm.Service{ID: service.ID, Spec: service.ServiceSpec}, nil)
				result, err := services.New(manager, &registry.NopAuthManager{}).Apply(t.Context(), "app", []dockerswarm.ServiceSpec{expected}, services.ApplyOptions{})
				require.NoError(t, err)
				require.Empty(t, result.ChangedServices)
			}
			var progress ServiceProgress
			request.Progress = func(p ServiceProgress) { progress = p }
			result, err := NewServiceWaiter(manager, nil, time.Millisecond, 20*time.Millisecond).Wait(t.Context(), request)
			switch tc.want {
			case ServiceReady:
				require.NoError(t, err)
				assert.Empty(t, result.UnresolvedServices)
				assert.Equal(t, ServiceReady, progress.State)
			case ServiceFailed:
				require.ErrorIs(t, err, ErrConvergenceFailed)
			case ServicePending:
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.NotErrorIs(t, err, ErrConvergenceFailed)
			}
		})
	}
}
