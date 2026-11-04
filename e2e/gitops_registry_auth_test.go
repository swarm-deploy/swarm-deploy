package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmGitOpsRegistryRotationWithoutDeploymentOrInitJob(t *testing.T) {
	f, auth, metricRegistry := newRegistryLifecycleFixture(t)
	image := auth.host + "/" + f.stack + ":private"
	dockerFixtureCommand(t, f.ctx, "tag", "busybox:1.36", image)
	dockerFixtureCommand(t, f.ctx, "push", image)
	// The application is deliberately scaled to zero, so its real overlay
	// attachment is comparable without requiring VXLAN in this runner. The
	// actual Init Job executes on the declared external host network.
	manifest := fmt.Sprintf(`services:
  private:
    image: %s
    command: [sleep, '600']
    deploy: {replicas: 0}
    x-init-deploy-jobs:
      - name: once
        image: busybox:1.36
        command: [sh, -c, 'echo initial-deployment-only']
        networks: [hostnet]
networks:
  default:
    ipam:
      config:
        - subnet: %s
  hostnet: {external: true, name: host}
`, image, f.subnet)
	require.NoError(t, os.WriteFile(f.path, []byte(manifest), 0o600))
	f.commitManifest(t)
	f.reconcile(t)
	beforeDeployment := f.latest(t)
	require.Equal(t, deploymentmodel.Succeeded, beforeDeployment.Status)
	require.Len(t, beforeDeployment.Attempts, 1)
	require.Len(t, beforeDeployment.Attempts[0].InitJobs, 1)
	require.EqualValues(t, 1, registryInitJobRuns(t, metricRegistry))
	// Zero replicas converge before the allocator necessarily finishes its
	// metadata writes. Settle that work before asserting no-op versions.
	_, err := f.observer.WaitStable(f.ctx, f.stack+"_private", time.Second)
	require.NoError(t, err)
	before := f.inspect(t, "private")
	beforeTasks := f.tasks(t, "private")
	require.Empty(t, beforeTasks)
	projection, err := f.state.Read(f.ctx)
	require.NoError(t, err)

	// These calls enter through the real GitOps Reconcile and the real
	// deployment controller; neither receives a new commit or Compose digest.
	f.reconcile(t)
	require.Equal(t, before.Version, f.inspect(t, "private").Version)
	auth.rotate(t, "rotated-token")
	f.reconcile(t)
	_, err = f.observer.WaitStable(f.ctx, f.stack+"_private", time.Second)
	require.NoError(t, err)
	after := f.inspect(t, "private")
	require.Greater(t, after.Version.Index, before.Version.Index, "unchanged GitOps must still send rotated auth")
	require.Equal(t, before.Spec, after.Spec)
	require.Equal(t, beforeTasks, f.tasks(t, "private"))
	for range 2 {
		f.reconcile(t)
	}
	require.Equal(t, after.Version, f.inspect(t, "private").Version, "unchanged token must not repeat writes")
	require.Equal(t, beforeDeployment, f.latest(t), "maintenance must preserve real deployment/attempt/init-job history")
	page, err := f.store.List(f.ctx, deploymentmodel.ListFilter{Stacks: []string{f.stack}})
	require.NoError(t, err)
	require.Len(t, page.Deployments, 1)
	require.EqualValues(t, 1, registryInitJobRuns(t, metricRegistry), "the actual Init Job must execute only once")
	currentProjection, err := f.state.Read(f.ctx)
	require.NoError(t, err)
	require.Equal(t, projection.Stacks[f.stack].LastDeployAt, currentProjection.Stacks[f.stack].LastDeployAt)

	// Request a real worker execution after proving the no-op lifecycle. Its
	// cache miss and authenticated registry access prove Engine stored the
	// credentials written by the preceding unchanged GitOps reconciliation.
	evictRegistryFixtureReference(t, f.externalSwarmFixture, image, after.Spec.TaskTemplate.ContainerSpec.Image)
	acceptedBeforePull := auth.accepted.Load()
	host, err := f.sw.Networks.Get(f.ctx, "host")
	require.NoError(t, err)
	f.poll(t, "external host-network execution accepted", func() bool {
		current := f.inspect(t, "private")
		require.Equal(t, after.ID, current.ID)
		one := uint64(1)
		current.Spec.Mode.Replicated.Replicas = &one
		current.Spec.TaskTemplate.Networks[0].Target = host.ID
		return f.sw.Services.Update(f.ctx, current.ID, current.Version, current.Spec,
			dockerswarm.ServiceUpdateOptions{RegistryAuthFrom: dockerswarm.RegistryAuthFromSpec}) == nil
	})
	f.poll(t, "worker pulled with GitOps-refreshed stored credentials", func() bool {
		if auth.accepted.Load() <= acceptedBeforePull {
			return false
		}
		for _, task := range f.tasks(t, "private") {
			if task.CurrentState == swarm.TaskStateRunning {
				return true
			}
		}
		return false
	})
}

func newRegistryLifecycleFixture(t *testing.T) (*lifecycleFixture, *authenticatedRegistry, *prometheus.Registry) {
	t.Helper()
	base := newExternalSwarmFixtureWithTimeout(t, 4*time.Minute)
	auth := newAuthenticatedRegistry(t, base.ctx)
	group := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "gitops_registry_e2e"})
	metricRegistry := prometheus.NewRegistry()
	metricRegistry.MustRegister(group)
	base.deployer = deployer.NewDeployer(100*time.Millisecond, time.Minute, base.docker, base.sw, group.Deploys,
		deployer.NewSecretResolver(deployer.PlaintextDecoder{}))
	dir := filepath.Dir(base.path)
	gitCommand(t, base.ctx, dir, "init")
	gitCommand(t, base.ctx, dir, "remote", "add", "origin", dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	f := newLifecycleComponents(t, base, cfg, gitx.NewRepository(config.GitSpec{Repository: dir}, dir))
	return f, auth, metricRegistry
}

func registryInitJobRuns(t *testing.T, registry *prometheus.Registry) float64 {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	var total float64
	for _, family := range families {
		if strings.HasSuffix(family.GetName(), "_deploys_init_job_runs_total") {
			for _, metric := range family.Metric {
				total += metric.GetCounter().GetValue()
			}
		}
	}
	return total
}
