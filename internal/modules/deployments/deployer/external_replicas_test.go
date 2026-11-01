package deployer

import (
	"fmt"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestBlockedJobFailsDeploymentWhileApplyingIndependentService(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			observer := NewMockServiceWaitExecutor(ctrl)
			current := nativeMinimalServiceSpec("job")
			current.Labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = "false"
			one := uint64(1)
			current.Mode.Replicated.Replicas = &one
			observer.EXPECT().Snapshot(gomock.Any(), "demo").Return([]swarm.StackService{
				{ID: "job-id", Name: "job", Mode: "replicated", ServiceSpec: current, Replicas: &one},
			}, nil)
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(dockerswarm.Service{ID: "job-id", Spec: current}, nil)
			if !single {
				manager.EXPECT().Inspect(gomock.Any(), "demo_web").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
				manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("web-id", nil)
			}
			d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: observer}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			desired := minimalCompose("job", "web")
			desired.Services[0].Deploy.Labels = *compose.NewLabels(map[string]string{
				labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false",
			})
			desired.Services[0].Environment = compose.Environment{Map: map[string]string{"CHANGED": "yes"}, Keys: []string{"CHANGED"}}
			var result Result
			var err error
			if single {
				result, err = d.DeployService(t.Context(), ServiceRequest{StackName: "demo", ServiceName: "job", Desired: desired})
				assert.Equal(t, stepApplyService, FailedStep(err))
			} else {
				result, err = d.DeployStack(t.Context(), StackRequest{StackName: "demo", Desired: desired})
				assert.Equal(t, stepApplyStack, FailedStep(err))
			}
			require.ErrorIs(t, err, services.ErrUpdateBlocked)
			assert.ErrorContains(t, err, "service job")
			assert.False(t, result.Verification.Known, "apply failure must not be represented as verified success")
			assert.Empty(t, result.SpecificationOnlyServices)
		})
	}
}

func TestIdleExternalTemplateUpdateReportsSpecificationOnly(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := time.Now().UTC()
			zero := uint64(0)
			current := nativeMinimalServiceSpec("job")
			current.Mode.Replicated.Replicas = &zero
			current.Labels[labelsdict.ServiceSyncPolicyReplicasManagedLabelKey] = "false"
			current.TaskTemplate.ForceUpdate = 12
			before := swarm.StackService{ID: "job-id", Name: "job", Mode: "replicated", Replicas: &zero,
				ServiceSpec: current, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return([]swarm.StackService{before}, nil)
			manager.EXPECT().Inspect(gomock.Any(), "demo_job").Return(dockerswarm.Service{ID: "job-id", Spec: current}, nil)
			manager.EXPECT().ListTasks(gomock.Any(), "job-id").Return(nil, nil).Times(2)
			after := before
			manager.EXPECT().Update(gomock.Any(), "job-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
					assert.Zero(t, *spec.Mode.Replicated.Replicas)
					assert.Equal(t, uint64(12), spec.TaskTemplate.ForceUpdate)
					after.ServiceSpec = spec
					after.UpdatedAt = now
					return nil
				})
			manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ any, _ swarm.ListServicesFilter) ([]swarm.StackService, error) {
					return []swarm.StackService{after}, nil
				})
			d := &Deployer{secretResolver: newTestSecretResolver(),
				serviceWaiter: waiterpkg.NewServiceWaiter(manager, time.Millisecond, time.Second)}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			desired := minimalCompose("job")
			desired.Services[0].Deploy.Replicas = &zero
			desired.Services[0].Deploy.Labels = *compose.NewLabels(map[string]string{
				labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false",
			})
			desired.Services[0].Environment = compose.Environment{Map: map[string]string{"CHANGED": "yes"}, Keys: []string{"CHANGED"}}
			var result Result
			var err error
			if single {
				result, err = d.DeployService(t.Context(), ServiceRequest{StackName: "demo", ServiceName: "job", Desired: desired})
			} else {
				result, err = d.DeployStack(t.Context(), StackRequest{StackName: "demo", Desired: desired})
			}
			require.NoError(t, err)
			assert.True(t, result.Verification.Known)
			assert.Empty(t, result.Verification.UnresolvedServices)
			assert.Equal(t, []string{"job"}, result.SpecificationOnlyServices)
		})
	}
}
