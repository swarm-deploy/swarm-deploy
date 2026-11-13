package e2e

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmEndpointUpdates(t *testing.T) {
	for _, publishMode := range []dockerswarm.PortConfigPublishMode{dockerswarm.PortConfigPublishModeHost, dockerswarm.PortConfigPublishModeIngress} {
		t.Run(string(publishMode), func(t *testing.T) {
			f := newExternalSwarmFixture(t)
			one := uint64(1)
			grace := 100 * time.Millisecond
			spec := dockerswarm.ServiceSpec{
				Annotations:  dockerswarm.Annotations{Name: f.stack + "_job", Labels: map[string]string{labelsdict.StackNamespace: f.stack, "com.docker.stack.image": "busybox:1.36"}},
				TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "busybox:1.36", Command: []string{"sleep", "600"}, StopGracePeriod: &grace}},
				Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &one}},
				UpdateConfig: &dockerswarm.UpdateConfig{Parallelism: 1, Monitor: 2 * time.Second, FailureAction: dockerswarm.UpdateFailureActionPause},
				EndpointSpec: &dockerswarm.EndpointSpec{Ports: []dockerswarm.PortConfig{{TargetPort: 80, PublishedPort: 32201, PublishMode: publishMode}}},
			}
			applier := services.New(f.sw.Services, &registry.NopAuthManager{})
			apply := func() {
				t.Helper()
				baseline, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
				require.NoError(t, err)
				applied, err := applier.Apply(f.ctx, f.stack, []dockerswarm.ServiceSpec{spec}, services.ApplyOptions{})
				require.NoError(t, err)
				result, err := waiter.NewServiceWaiter(f.sw.Services, f.sw.Nodes, 20*time.Millisecond, 30*time.Second).Wait(f.ctx, waiter.ServiceWaitRequest{
					StackName: f.stack, ServiceNames: []string{"job"}, Baseline: baseline, AppliedServices: applied.ChangedServices,
					ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"job": spec}, UpdateGenerations: applied.UpdateGenerations,
				})
				require.NoError(t, err)
				require.Empty(t, result.UnresolvedServices)
			}
			apply()
			original := endpointRunningTasks(t, f)
			require.Len(t, original, 1)
			// TaskTemplate is identical; endpoint changes alone must wait for new tasks
			// AND completion of the rollout's monitoring period.
			spec = f.inspect(t, "job").Spec
			spec.EndpointSpec.Ports[0].PublishedPort++
			apply()
			replaced := endpointRunningTasks(t, f)
			require.Len(t, replaced, 1)
			assert.NotEqual(t, original[0].ID, replaced[0].ID)
			completed := f.inspect(t, "job")
			require.NotNil(t, completed.UpdateStatus)
			require.Equal(t, dockerswarm.UpdateStateCompleted, completed.UpdateStatus.State)
			require.NotNil(t, completed.UpdateStatus.StartedAt)
			assert.False(t, replaced[0].CreatedAt.Before(*completed.UpdateStatus.StartedAt))
			spec = f.inspect(t, "job").Spec
			spec.Labels["release"] = "metadata"
			apply()
			preserved := endpointRunningTasks(t, f)
			require.Len(t, preserved, 1)
			assert.Equal(t, replaced[0].ID, preserved[0].ID)
			// SwarmKit's placement shortcut takes precedence over endpoint differences.
			spec = f.inspect(t, "job").Spec
			spec.EndpointSpec.Ports[0].PublishedPort++
			spec.TaskTemplate.Placement = &dockerswarm.Placement{Constraints: []string{"node.id == " + replaced[0].Node}}
			apply()
			preserved = endpointRunningTasks(t, f)
			require.Len(t, preserved, 1)
			assert.Equal(t, replaced[0].ID, preserved[0].ID)
			assert.Nil(t, f.inspect(t, "job").UpdateStatus)
			zero := uint64(0)
			spec = f.inspect(t, "job").Spec
			spec.Mode.Replicated.Replicas = &zero
			apply()
			spec = f.inspect(t, "job").Spec
			spec.EndpointSpec.Ports[0].PublishedPort++
			apply()
			assert.Nil(t, f.inspect(t, "job").UpdateStatus, "zero replicas have no dirty task slots to roll")
		})
	}
}

func TestRealSwarmEndpointControllerRecovery(t *testing.T) {
	for _, followup := range []string{"no-op", "labels", "credentials"} {
		t.Run(followup, func(t *testing.T) { testEndpointControllerRecovery(t, followup) })
	}
}

func testEndpointControllerRecovery(t *testing.T, followup string) {
	f := newLifecycleFixture(t, false)
	load := func(port int) compose.File {
		file := f.manifest(t, fmt.Sprintf(`  job:
    image: busybox:1.36
    command: ["sleep", "600"]
    stop_grace_period: 100ms
    ports:
      - {target: 80, published: %d, mode: host}
    deploy:
      replicas: 1
      update_config: {monitor: 10s, failure_action: pause}
`, port))
		// Single-node runners without VXLAN may provide an existing Swarm network.
		if network := os.Getenv("SWARM_DEPLOY_E2E_NETWORK"); network != "" {
			file.Compose.Networks = map[string]compose.Network{"default": {External: true, Name: network}}
		}
		return file
	}
	req := deployments.ExecuteRequest{Stack: config.StackSpec{Name: f.stack}, Commit: "initial", Desired: load(32211), SourceChanged: true}
	initial, err := f.executor.Execute(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, deploymentmodel.Succeeded, initial.Status)
	original := endpointRunningTasks(t, f.externalSwarmFixture)
	require.Len(t, original, 1)
	req.Commit, req.Desired = "endpoint", load(32212)
	lifecycle := deployments.NewService(f.db, f.bus)
	attempt, err := lifecycle.StartIfChanged(f.ctx, f.stack, req.Commit, req.Desired, true)
	require.NoError(t, err)
	require.NotNil(t, attempt)
	converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, req.Desired.Compose)
	require.NoError(t, err)
	applied, err := services.New(f.sw.Services, &registry.NopAuthManager{}).Apply(f.ctx, f.stack, converted.Services, services.ApplyOptions{
		BeforeUpdate: func(ctx context.Context, name string, generation services.UpdateGeneration) (func(context.Context) error, error) {
			return lifecycle.RecordUpdateGeneration(ctx, attempt.ID, name, generation)
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"job"}, applied.ChangedServices)
	accepted := f.inspect(t, "job")
	// CI runners can have registry credentials. Isolate the actual no-op and
	// label-only cases from the separate credential-refresh scenario below.
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	if followup != "no-op" {
		// Force the important clean-slots case: the first rollout has replacement
		// tasks, but its monitor has not completed when the second write arrives.
		f.poll(t, "replacement running while endpoint rollout is still updating", func() bool {
			service := f.inspect(t, "job")
			tasks := endpointRunningTasks(t, f.externalSwarmFixture)
			return service.UpdateStatus != nil && service.UpdateStatus.State == dockerswarm.UpdateStateUpdating &&
				len(tasks) == 1 && tasks[0].ID != original[0].ID
		})
		if followup == "labels" {
			req.Desired.Compose.Services[0].Deploy.Labels = *compose.NewLabels(map[string]string{"release": "metadata"})
			req.Commit = "metadata"
		} else {
			// The image is already cached by digest; this exercises the independent
			// credential write without requiring a private registry or changing tasks.
			t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"docker.io":{"username":"e2e","password":"unused"}}}`)
		}
	}
	auth, err := registry.NewAuthManager().ResolveImage(converted.Services[0].TaskTemplate.ContainerSpec.Image)
	require.NoError(t, err)
	t.Logf("recovery: matching spec=%t, nonempty registry credentials=%t", services.SpecsEqual(accepted.Spec, converted.Services[0]), auth != "")
	// Simulate losing the controller immediately after Docker accepts the write:
	// leave the Attempt running, then rebuild execution components from SQLite.
	freshDeployer := deployer.NewDeployer(20*time.Millisecond, 40*time.Second, f.sw, nil, deployer.NewSecretResolver(deployer.PlaintextDecoder{}))
	recovered := deployments.NewController(deployments.NewService(f.db, f.bus), freshDeployer, f.sw.Services,
		pruner.NewServicePruner(f.sw.Services, f.bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
	require.NoError(t, recovered.Recover(f.ctx))
	stored := f.latest(t)
	require.Equal(t, applied.UpdateGenerations["job"], stored.Verification.Generations["job"])
	req.ManualRetry = true
	result, err := recovered.Execute(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, deploymentmodel.Succeeded, result.Status)
	finished := f.inspect(t, "job")
	if followup == "no-op" {
		require.NotNil(t, finished.UpdateStatus, "a true no-op must retain the original rollout status")
		require.Equal(t, dockerswarm.UpdateStateCompleted, finished.UpdateStatus.State)
		require.NotNil(t, finished.UpdateStatus.StartedAt)
		assert.True(t, finished.UpdateStatus.StartedAt.After(stored.Verification.Generations["job"].After))
	} else {
		// A metadata/auth write can cancel the original monitor and leave clean
		// slots without any status. Replacement tasks must still be proven below.
		require.NotNil(t, finished.PreviousSpec)
		require.Nil(t, finished.UpdateStatus, "non-replacing follow-up writes leave clean task slots without status")
		assert.Equal(t, finished.Spec.EndpointSpec, finished.PreviousSpec.EndpointSpec)
	}
	assert.Equal(t, accepted.Spec.TaskTemplate.ForceUpdate, finished.Spec.TaskTemplate.ForceUpdate, "recovery must not force a task restart")
	tasks := endpointRunningTasks(t, f.externalSwarmFixture)
	require.Len(t, tasks, 1)
	assert.NotEqual(t, original[0].ID, tasks[0].ID)
	cutoff := accepted.UpdatedAt
	if accepted.UpdateStatus != nil && accepted.UpdateStatus.StartedAt != nil {
		cutoff = *accepted.UpdateStatus.StartedAt
	}
	assert.False(t, tasks[0].CreatedAt.Before(cutoff), "task must belong to the accepted endpoint generation")
	assert.Empty(t, f.latest(t).Verification.Generations)
}

func endpointRunningTasks(t *testing.T, f *externalSwarmFixture) []swarm.ServiceTask {
	t.Helper()
	return slices.DeleteFunc(f.tasks(t, "job"), func(task swarm.ServiceTask) bool {
		return task.DesiredState != swarm.TaskStateRunning || task.CurrentState != swarm.TaskStateRunning
	})
}
