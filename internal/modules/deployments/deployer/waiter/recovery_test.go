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

func TestInitialDeploymentRecoveryAfterAllocation(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
	}{
		{name: "allocation metadata", age: time.Millisecond},
		{name: "recovery long after creation", age: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "nginx:alpine")
			service.UpdatedAt = service.CreatedAt.Add(tc.age)
			tasks := testRunningTasks(1, service.CreatedAt)
			for _, unresolved := range []bool{false, true} {
				progress := evaluateService("api", service, service, tasks, unresolved, service.ServiceSpec, nil)
				assert.Equal(t, ServiceReady, progress.State, progress.Message)
			}
		})
	}
}

func TestInitialDeploymentInterruptedThenRepeatedSyncDoesNotForceUpdate(t *testing.T) {
	interruptedManager := swarm.NewMockServiceManager(gomock.NewController(t))
	service := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "nginx:alpine")
	service.ServiceSpec.Labels = map[string]string{labelsdict.StackNamespace: "app"}
	service.ServiceSpec.TaskTemplate.RestartPolicy = &dockerswarm.RestartPolicy{Condition: dockerswarm.RestartPolicyConditionNone}
	service.UpdatedAt = service.CreatedAt.Add(time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancellation and the poll ticker may become ready together. Keep canceled
	// observations repeatable, then use a fresh manager for the recovered process.
	interruptedManager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil).AnyTimes()
	interruptedManager.EXPECT().ListTasks(gomock.Any(), service.ID).DoAndReturn(func(context.Context, string) ([]swarm.ServiceTask, error) {
		cancel()
		return []swarm.ServiceTask{testTask("starting", 1, swarm.TaskStateRunning, swarm.TaskStateStarting, service.CreatedAt)}, nil
	}).AnyTimes()
	request := ServiceWaitRequest{StackName: "app", ServiceNames: []string{"api"}, Baseline: []swarm.StackService{service}}
	result, err := newTestServiceWaiter(interruptedManager).Wait(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"api"}, result.UnresolvedServices)
	request.UnresolvedServices = result.UnresolvedServices
	tasks := testRunningTasks(1, service.CreatedAt)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	applier := services.New(manager, &registry.NopAuthManager{})
	for _, manual := range []bool{false, true, false, true} {
		manager.EXPECT().Inspect(gomock.Any(), service.ServiceSpec.Name).Return(dockerswarm.Service{
			ID: service.ID, Spec: service.ServiceSpec,
			Meta: dockerswarm.Meta{CreatedAt: service.CreatedAt, UpdatedAt: service.UpdatedAt},
		}, nil)
		if manual {
			manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return(tasks, nil)
		}
		applied, err := applier.Apply(t.Context(), "app", []dockerswarm.ServiceSpec{service.ServiceSpec}, services.ApplyOptions{RetryFailedUpdates: manual})
		require.NoError(t, err)
		require.Empty(t, applied.ChangedServices, "healthy tasks must not be restarted, including Manual Sync")
		manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{service}, nil)
		manager.EXPECT().ListTasks(gomock.Any(), service.ID).Return(tasks, nil)
		result, err = newTestServiceWaiter(manager).Wait(t.Context(), request)
		require.NoError(t, err)
		assert.Empty(t, result.UnresolvedServices)
	}
}

func TestUnresolvedRollingUpdateCannotUseCreationGeneration(t *testing.T) {
	for _, tc := range []struct {
		name                                                 string
		changeTemplate, previousSpec, previousStatus, forced bool
	}{
		{name: "new task template", changeTemplate: true},
		{name: "recovered update with previous spec", previousSpec: true},
		{name: "missing current status after earlier update", previousStatus: true},
		{name: "forced retry before status arrives", forced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:1")
			baseline.UpdatedAt = baseline.CreatedAt.Add(time.Millisecond)
			service := baseline
			if tc.changeTemplate {
				service = testReplicatedService("api", "api-id", 1, serviceWaitTestTime, "api:2")
				service.UpdatedAt = serviceWaitTestTime.Add(time.Minute)
			}
			if tc.previousSpec {
				service.PreviousSpec = &baseline.ServiceSpec
				baseline.PreviousSpec = service.PreviousSpec
			}
			if tc.previousStatus {
				baseline.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: baseline.CreatedAt}
			}
			if tc.forced {
				service.ServiceSpec.TaskTemplate.ForceUpdate = 1
				baseline.ServiceSpec.TaskTemplate.ForceUpdate = 1
			}
			progress := evaluateService(
				"api", baseline, service, testRunningTasks(1, service.CreatedAt), true, service.ServiceSpec, nil,
			)
			assert.Equal(t, ServicePending, progress.State, "old running tasks cannot verify a rolling update")
		})
	}
}
