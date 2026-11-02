package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmNamedVolumeAndRevertAfterRollback(t *testing.T) {
	f := newExternalSwarmFixture(t)
	// Host networking keeps this single-node regression independent of VXLAN.
	load := func(command string) compose.Compose {
		manifest := fmt.Sprintf(`services:
  db:
    image: busybox:1.36
    command: ["sh", "-c", %q]
    stop_grace_period: 1s
    volumes: ["db.data:/data"]
    deploy:
      replicas: 1
      restart_policy: {condition: none}
      update_config: {failure_action: rollback, monitor: 1s}
      rollback_config: {monitor: 1s}
volumes:
  db.data:
networks:
  default: {external: true, name: host}
`, command)
		require.NoError(t, os.WriteFile(f.path, []byte(manifest), 0o600))
		file, err := compose.NewFileLoader().Load(f.ctx, f.path)
		require.NoError(t, err)
		return file.Compose
	}
	// Exercise the native converter, applier and waiter against the real Engine.
	// Engine rewrites built-in host attachments to a Swarm-internal network ID.
	// Reuse that attachment in this host-only fixture; production remains unchanged.
	var hostTarget string
	apply := func(desired compose.Compose, unresolved []string) (services.ApplyResult, error) {
		t.Helper()
		converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, desired)
		require.NoError(t, err)
		spec := converted.Services[0]
		if hostTarget != "" {
			spec.TaskTemplate.Networks[0].Target = hostTarget
		}
		baseline, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
		require.NoError(t, err)
		applied, err := services.New(f.sw.Services, &registry.NopAuthManager{}).Apply(f.ctx, f.stack,
			[]dockerswarm.ServiceSpec{spec}, services.ApplyOptions{})
		require.NoError(t, err)
		result, waitErr := waiter.NewServiceWaiter(f.sw.Services, 100*time.Millisecond, 30*time.Second).Wait(f.ctx, waiter.ServiceWaitRequest{
			StackName: f.stack, ServiceNames: []string{"db"}, Baseline: baseline,
			AppliedServices: applied.ChangedServices, UnresolvedServices: unresolved,
			ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"db": spec},
		})
		if waitErr == nil {
			require.Empty(t, result.UnresolvedServices)
		}
		return applied, waitErr
	}
	// Registered after fixture cleanup, this runs first and removes the service
	// before deleting only the volume created by this test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if service, err := f.sw.Services.Inspect(ctx, f.stack+"_db"); err == nil {
			assert.NoError(t, f.sw.Services.Remove(ctx, service.ID))
		}
		assert.Eventually(t, func() bool {
			return exec.CommandContext(ctx, "docker", "volume", "rm", f.stack+"_db.data").Run() == nil
		}, 20*time.Second, 100*time.Millisecond)
	})
	_, err := apply(load("echo preserved > /data/sentinel; sleep 600"), nil)
	require.NoError(t, err)
	initial := f.inspect(t, "db")
	hostTarget = initial.Spec.TaskTemplate.Networks[0].Target
	require.Len(t, initial.Spec.TaskTemplate.ContainerSpec.Mounts, 1)
	assert.Equal(t, mount.TypeVolume, initial.Spec.TaskTemplate.ContainerSpec.Mounts[0].Type)
	assert.Equal(t, f.stack+"_db.data", initial.Spec.TaskTemplate.ContainerSpec.Mounts[0].Source)
	// Updating the service reads data written by the preceding task.
	a := load("grep -qx preserved /data/sentinel || exit 42; sleep 601")
	_, err = apply(a, nil)
	require.NoError(t, err)
	updated := f.inspect(t, "db")
	assert.Equal(t, initial.Spec.TaskTemplate.ContainerSpec.Mounts, updated.Spec.TaskTemplate.ContainerSpec.Mounts)
	b := load("exit 23")
	_, err = apply(b, nil)
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	require.Eventually(t, func() bool {
		service, inspectErr := f.sw.Services.Inspect(f.ctx, f.stack+"_db")
		return inspectErr == nil && service.UpdateStatus != nil && service.UpdateStatus.State == "rollback_completed"
	}, 30*time.Second, 100*time.Millisecond)
	_, err = f.observer.WaitStable(f.ctx, f.stack+"_db", time.Second)
	require.NoError(t, err)
	before := f.inspect(t, "db")
	tasks, err := f.observer.WaitRunningTasks(f.ctx, before.ID, 1)
	require.NoError(t, err)
	converted, err := converter.New(f.sw.Networks, f.sw.Configs, f.sw.Secrets).Convert(f.ctx, f.stack, b)
	require.NoError(t, err)
	baseline, err := f.sw.Services.ListServices(f.ctx, swarm.ListServicesFilter{Stack: f.stack})
	require.NoError(t, err)
	// A healthy rolled-back service cannot satisfy B, even without another write.
	_, err = waiter.NewServiceWaiter(f.sw.Services, 100*time.Millisecond, 5*time.Second).Wait(f.ctx, waiter.ServiceWaitRequest{
		StackName: f.stack, ServiceNames: []string{"db"}, Baseline: baseline,
		UnresolvedServices: []string{"db"}, ExpectedSpecs: map[string]dockerswarm.ServiceSpec{"db": converted.Services[0]},
	})
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	applied, err := apply(a, []string{"db"})
	require.NoError(t, err)
	require.Empty(t, applied.ChangedServices, "Git revert must be a no-op apply")
	after := f.inspect(t, "db")
	assert.Equal(t, before.Version, after.Version, "Git revert must converge without a Docker update")
	assert.Equal(t, before.Spec.TaskTemplate.ForceUpdate, after.Spec.TaskTemplate.ForceUpdate)
	assert.True(t, services.SpecsEqual(before.Spec, after.Spec))
	afterTasks, err := f.observer.WaitRunningTasks(f.ctx, after.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, tasks, afterTasks, "the healthy restored task must not restart")
}
