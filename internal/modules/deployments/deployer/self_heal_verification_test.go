package deployer

import (
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	networkspkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/networks"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDeployServiceVerifiesUnchangedTasksWithoutApplying(t *testing.T) {
	for _, tc := range []struct {
		name       string
		running    bool
		external   bool
		normal     bool
		unexpected bool
	}{
		{name: "failed task is not convergence"},
		{name: "actual recovery", running: true},
		{name: "externally managed tasks remain specification only", external: true},
		{name: "normal remediation no-op still verifies tasks", normal: true},
		{name: "healthy tasks for unexpected specification do not establish recovery", running: true, unexpected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			networks := swarm.NewMockNetworkManager(ctrl)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{
				ID: "network-id", Name: "demo_default", Scope: "swarm", Driver: "overlay",
				Labels: map[string]string{labelsdict.StackNamespace: "demo"},
			}, nil).AnyTimes()
			configs := swarm.NewMockConfigManager(ctrl)
			secrets := swarm.NewMockSecretManager(ctrl)
			desired := minimalCompose("api")
			desired.Services[0].Deploy.RestartPolicy = &compose.ServiceDeployRestartPolicy{Condition: "none"}
			if tc.external {
				desired.Services[0].Deploy.Labels = *compose.NewLabels(map[string]string{
					labelsdict.ServiceSyncPolicyReplicasManagedLabelKey: "false",
				})
			}
			c := converter.New(networks, configs, secrets)
			converted, err := c.Convert(t.Context(), "demo", desired)
			require.NoError(t, err)
			spec := converted.Services[0]
			one := uint64(1)
			now := time.Now()
			live := swarm.StackService{ID: "api-id", Name: "api", Mode: "replicated", Replicas: &one,
				ServiceSpec: spec, CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
			if tc.unexpected {
				container := *spec.TaskTemplate.ContainerSpec
				container.Image = "unrelated:1"
				live.ServiceSpec.TaskTemplate.ContainerSpec = &container
			}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).
				Return([]swarm.StackService{live}, nil).AnyTimes()
			exit := 1
			task := swarm.ServiceTask{ID: "task", Slot: 1, CreatedAt: now, UpdatedAt: now,
				DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateFailed,
				ExitCode: &exit, Spec: swarm.NewTaskSpec(live.ServiceSpec.TaskTemplate)}
			if tc.running {
				task.CurrentState = swarm.TaskStateRunning
			}
			manager.EXPECT().ListTasks(gomock.Any(), live.ID).Return([]swarm.ServiceTask{task}, nil).AnyTimes()
			if !tc.running && !tc.external {
				manager.EXPECT().TaskLogs(gomock.Any(), task.ID, gomock.Any()).Return(nil, nil, assert.AnError)
			}
			d := &Deployer{converter: c, services: manager,
				serviceWaiter: waiter.NewServiceWaiter(manager, nil, time.Millisecond, 30*time.Millisecond)}
			if tc.normal {
				d.networks = networkspkg.New(networks)
				d.serviceApplier = services.New(manager, &registry.NopAuthManager{})
				manager.EXPECT().Inspect(gomock.Any(), spec.Name).Return(dockerswarm.Service{ID: live.ID, Spec: spec}, nil)
			}
			result, err := d.DeployService(t.Context(), ServiceRequest{
				StackName: "demo", ServiceName: "api", Desired: desired, VerifyOnly: !tc.normal,
			})
			if tc.unexpected {
				require.Error(t, err)
				assert.NotErrorIs(t, err, waiter.ErrConvergenceFailed)
				assert.Equal(t, []string{"api"}, result.Verification.UnresolvedServices)
			} else if tc.running || tc.external {
				require.NoError(t, err)
				assert.Empty(t, result.Verification.UnresolvedServices)
			} else {
				require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
				assert.Equal(t, []string{"api"}, result.Verification.UnresolvedServices)
			}
			if tc.external {
				assert.Equal(t, []string{"api"}, result.SpecificationOnlyServices)
			}
			// No mutating calls are expected, including after a no-op normal apply.
			assert.Equal(t, dockerswarm.RestartPolicyConditionNone, spec.TaskTemplate.RestartPolicy.Condition)
		})
	}
}
