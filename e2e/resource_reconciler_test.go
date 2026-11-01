package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// TestDeployStackOnRealSwarm exercises the actual Docker Engine API and a
// running Swarm task. It must not be replaced with a mocked Docker API fixture.
func TestDeployStackOnRealSwarm(t *testing.T) {
	if os.Getenv("SWARM_DEPLOY_E2E") != "1" {
		t.Skip("set SWARM_DEPLOY_E2E=1 to run against a real initialized Docker Swarm")
	}

	dockerPath, err := exec.LookPath("docker")
	require.NoError(t, err)

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dockerClient.Close()) })

	stack := fmt.Sprintf("sdreconcile%d", time.Now().UnixNano())
	dir := t.TempDir()
	swarmService := swarm.NewSwarm(dockerClient)
	stackDeployer := deployer.NewDeployer(100*time.Millisecond, 2*time.Minute, dockerClient, swarmService, nil, deployer.NewSecretResolver(fs.NewLocalFileSystem(), deployer.PlaintextDecoder{}))
	composePath := filepath.Join(dir, "compose.yaml")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(ctx, dockerPath, "stack", "rm", stack).CombinedOutput(); err != nil {
			t.Logf("stack cleanup: %v: %s", err, output)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()

	// Initial deploy must create real Docker resources without Init Jobs.
	writeRealSwarmFixture(t, dir, composePath, "", "", "secret-v1", "config-v1", "mounted-v1")
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	secretID1, configID1 := inspectRealSwarmResources(t, ctx, dockerClient, dockerPath, stack, stack+"_password", stack+"_settings")
	waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", "mounted-v1")
	serviceBefore, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	runningBefore, tasksBefore := realSwarmTaskIDs(t, ctx, swarmService, serviceBefore.ID)

	// Deploying the same stack again must reuse IDs and keep the task healthy.
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	secretIDAgain, configIDAgain := inspectRealSwarmResources(t, ctx, dockerClient, dockerPath, stack, stack+"_password", stack+"_settings")
	assert.Equal(t, secretID1, secretIDAgain, "unchanged secret must be reused")
	assert.Equal(t, configID1, configIDAgain, "unchanged config must be reused")
	serviceAgain, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	assert.Equal(t, serviceBefore.ID, serviceAgain.ID)
	assert.Equal(t, serviceBefore.Version.Index, serviceAgain.Version.Index, "unchanged deploy must not update the service")
	assert.Equal(t, serviceBefore.UpdateStatus, serviceAgain.UpdateStatus, "unchanged deploy must not start a rolling update")
	runningAgain, tasksAgain := realSwarmTaskIDs(t, ctx, swarmService, serviceAgain.ID)
	assert.Equal(t, runningBefore, runningAgain, "running tasks must not be replaced")
	assert.Equal(t, tasksBefore, tasksAgain, "unchanged deploy must not schedule any new tasks")

	t.Run("ignored Swarm options do not roll services", func(t *testing.T) {
		prepared, err := compose.NewFileLoader().Load(ctx, composePath)
		require.NoError(t, err)
		for _, options := range []map[string]interface{}{
			{"privileged": true, "depends_on": []string{"unused"}, "restart": "always"},
			{"privileged": false, "depends_on": map[string]interface{}{"unused": map[string]interface{}{"condition": "service_healthy"}}, "restart": "on-failure"},
			nil,
		} {
			prepared.Compose.Services[0].Extra = options
			_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
				StackName:         stack,
				SourceComposePath: composePath,
				Desired:           prepared.Compose,
			})
			require.NoError(t, err)
			current, err := swarmService.Services.Inspect(ctx, serviceBefore.ID)
			require.NoError(t, err)
			assert.Equal(t, serviceBefore.Version.Index, current.Version.Index)
			assert.Equal(t, serviceBefore.Spec, current.Spec)
			running, tasks := realSwarmTaskIDs(t, ctx, swarmService, current.ID)
			assert.Equal(t, runningBefore, running)
			assert.Equal(t, tasksBefore, tasks)
		}
	})

	// An external force update must be waited for, without resetting ForceUpdate
	// or issuing a second update of the unchanged Compose service.
	output, err := exec.CommandContext(ctx, dockerPath, "service", "update", "--detach=true", "--force", stack+"_check").CombinedOutput()
	require.NoError(t, err, "force update: %s", output)
	forced, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	require.Greater(t, forced.Spec.TaskTemplate.ForceUpdate, uint64(0))
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	afterForce, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	// Swarm advances the object version as this rollout's status progresses.
	// An application update would replace PreviousSpec as well as potentially
	// changing Spec, so compare those across the in-flight force rollout.
	assert.Equal(t, forced.Spec, afterForce.Spec, "waiting must preserve the externally forced specification")
	assert.Equal(t, forced.PreviousSpec, afterForce.PreviousSpec, "waiting must not submit another service update")
	require.NotNil(t, afterForce.UpdateStatus)
	assert.Equal(t, dockerswarm.UpdateStateCompleted, afterForce.UpdateStatus.State)
	runningBefore, tasksAfterForce := realSwarmTaskIDs(t, ctx, swarmService, afterForce.ID)

	// Once convergence is complete, idempotency also includes a stable object
	// version and the exact same running and historical task identities.
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	repeatedForce, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	assert.Equal(t, afterForce.Version.Index, repeatedForce.Version.Index)
	assert.Equal(t, afterForce.Spec, repeatedForce.Spec)
	runningRepeated, tasksRepeated := realSwarmTaskIDs(t, ctx, swarmService, repeatedForce.ID)
	assert.Equal(t, runningBefore, runningRepeated)
	assert.Equal(t, tasksAfterForce, tasksRepeated)

	// Immutable network option changes do not block application deployment.
	networkBefore, err := swarmService.Networks.Get(ctx, stack+"_default")
	require.NoError(t, err)
	prepared, err := compose.NewFileLoader().Load(ctx, composePath)
	require.NoError(t, err)
	prepared.Compose.Networks = map[string]compose.Network{"default": {
		Attachable: true, IPAM: &compose.NetworkIPAM{Config: []compose.NetworkIPAMPool{{Subnet: "10.249.0.0/24"}}},
	}, "unused": {External: true, Name: "must-not-exist"}}
	_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
		StackName:         stack,
		SourceComposePath: composePath,
		Desired:           prepared.Compose,
	})
	require.NoError(t, err)
	networkAfter, err := swarmService.Networks.Get(ctx, stack+"_default")
	require.NoError(t, err)
	assert.Equal(t, networkBefore.ID, networkAfter.ID)

	// Change only the container command, keeping the same secret/config IDs.
	writeRealSwarmFixture(t, dir, composePath, "", "", "secret-v1", "config-v1", "spec-updated")
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	serviceUpdated, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	assert.Equal(t, serviceBefore.ID, serviceUpdated.ID)
	assert.Greater(t, serviceUpdated.Version.Index, serviceAgain.Version.Index)
	require.NotNil(t, serviceUpdated.Spec.TaskTemplate.ContainerSpec)
	assert.Contains(t, serviceUpdated.Spec.TaskTemplate.ContainerSpec.Args[2], "spec-updated")
	waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", "spec-updated")
	runningUpdated, _ := realSwarmTaskIDs(t, ctx, swarmService, serviceUpdated.ID)
	assert.NotEqual(t, runningBefore, runningUpdated, "a real task specification change must replace the task")

	// Simulate the names produced by hash-based rotation and verify that
	// new resource versions are created and mounted in the new task.
	rotatedSecretName := stack + "-password-v2"
	rotatedConfigName := stack + "-settings-v2"
	writeRealSwarmFixture(t, dir, composePath, rotatedSecretName, rotatedConfigName, "secret-v2", "config-v2", "mounted-v2")
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	secretID2, configID2 := inspectRealSwarmResources(t, ctx, dockerClient, dockerPath, stack, rotatedSecretName, rotatedConfigName)
	assert.NotEqual(t, secretID1, secretID2, "rotated secret must be a different Docker object")
	assert.NotEqual(t, configID1, configID2, "rotated config must be a different Docker object")
	waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", "mounted-v2")
	// Explicitly retry an accepted failed rollout. It must create a new task
	// generation and verify its real failure, rather than replaying stale diagnostics.
	prepared, err = compose.NewFileLoader().Load(ctx, composePath)
	require.NoError(t, err)
	failing := prepared.Compose
	failing.Services[0].Command = compose.NewCommand([]string{"sh", "-c", "exit 1"})
	failing.Services[0].Deploy.UpdateConfig = &compose.ServiceDeployUpdateConfig{Monitor: "1s", FailureAction: "pause"}
	failing.Services[0].Deploy.RestartPolicy = &compose.ServiceDeployRestartPolicy{Condition: "none"}
	_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
		StackName:         stack,
		SourceComposePath: composePath,
		Desired:           failing,
	})
	require.Error(t, err)
	require.True(t, deployer.ConvergenceFailed(err))
	failed, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	require.NotNil(t, failed.UpdateStatus)
	require.Equal(t, dockerswarm.UpdateStatePaused, failed.UpdateStatus.State)
	_, retryErr := stackDeployer.DeployStack(ctx, deployer.StackRequest{
		StackName:         stack,
		SourceComposePath: composePath,
		Desired:           failing,
		Options:           deployer.StackOptions{RetryFailedUpdates: true},
	})
	require.Error(t, retryErr)
	assert.True(t, deployer.ConvergenceFailed(retryErr))
	assert.ErrorContains(t, retryErr, "latest task failure: id=")
	assert.ErrorContains(t, retryErr, "state=failed")
	retried, err := swarmService.Services.Inspect(ctx, stack+"_check")
	require.NoError(t, err)
	assert.Greater(t, retried.Version.Index, failed.Version.Index, "retry must send ServiceUpdate")
	assert.Equal(t, failed.Spec.TaskTemplate.ForceUpdate+1, retried.Spec.TaskTemplate.ForceUpdate)
	deployFixture(t, ctx, stackDeployer, stack, composePath)

	t.Run("mutable registry tag", func(t *testing.T) {
		registry := os.Getenv("SWARM_DEPLOY_E2E_REGISTRY")
		if registry == "" {
			t.Skip("set SWARM_DEPLOY_E2E_REGISTRY to a pushable registry reachable by Swarm nodes")
		}
		image := strings.TrimSuffix(registry, "/") + "/" + stack
		push := func(source, tag string) {
			for _, args := range [][]string{{"pull", source}, {"tag", source, image + ":" + tag}, {"push", image + ":" + tag}} {
				output, err := exec.CommandContext(ctx, dockerPath, args...).CombinedOutput()
				require.NoError(t, err, "docker %v: %s", args, output)
			}
		}
		push("busybox:1.36", "moving")
		prepared, err := compose.NewFileLoader().Load(ctx, composePath)
		require.NoError(t, err)
		desired := prepared.Compose
		desired.Services[0].Image = image + ":moving"
		_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
			StackName:         stack,
			SourceComposePath: composePath,
			Desired:           desired,
		})
		require.NoError(t, err)
		pinned, err := swarmService.Services.Inspect(ctx, stack+"_check")
		require.NoError(t, err)
		require.Contains(t, pinned.Spec.TaskTemplate.ContainerSpec.Image, "@sha256:")
		push("busybox:1.37", "moving")
		desired.Services[0].Environment.Map = map[string]string{"UNRELATED": "change"}
		_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
			StackName:         stack,
			SourceComposePath: composePath,
			Desired:           desired,
		})
		require.NoError(t, err)
		unchanged, err := swarmService.Services.Inspect(ctx, stack+"_check")
		require.NoError(t, err)
		assert.Equal(t, pinned.Spec.TaskTemplate.ContainerSpec.Image, unchanged.Spec.TaskTemplate.ContainerSpec.Image)
		push("busybox:1.37", "next")
		desired.Services[0].Image = image + ":next"
		_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
			StackName:         stack,
			SourceComposePath: composePath,
			Desired:           desired,
		})
		require.NoError(t, err)
		updated, err := swarmService.Services.Inspect(ctx, stack+"_check")
		require.NoError(t, err)
		assert.NotEqual(t, pinned.Spec.TaskTemplate.ContainerSpec.Image, updated.Spec.TaskTemplate.ContainerSpec.Image)
	})

}

func realSwarmTaskIDs(t *testing.T, ctx context.Context, service *swarm.Swarm, serviceID string) ([]string, []string) {
	t.Helper()
	tasks, err := service.Services.ListTasks(ctx, serviceID)
	require.NoError(t, err)
	var running, all []string
	for _, task := range tasks {
		all = append(all, task.ID)
		if task.CurrentState == swarm.TaskStateRunning && task.DesiredState == swarm.TaskStateRunning {
			running = append(running, task.ID)
		}
	}
	require.Len(t, running, 1, "fixture must have exactly one healthy running task")
	slices.Sort(running)
	slices.Sort(all)
	return running, all
}

func writeRealSwarmFixture(t *testing.T, dir, composePath, secretName, configName, secretValue, configValue, marker string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte(secretValue+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.txt"), []byte(configValue+"\n"), 0o600))

	secretNameYAML, configNameYAML := "", ""
	if secretName != "" {
		secretNameYAML = "    name: " + secretName + "\n"
	}
	if configName != "" {
		configNameYAML = "    name: " + configName + "\n"
	}

	manifest := fmt.Sprintf(`services:
  check:
    image: busybox:1.36
    user: '1000:1000'
    working_dir: /tmp
    hostname: compat-check
    read_only: true
    tmpfs: /tmp
    command: ["sh", "-ec", "grep -qx '%s' /run/secrets/password && grep -qx '%s' /etc/app.conf && echo %s && sleep 120"]
    secrets:
      - password
    configs:
      - source: settings
        target: /etc/app.conf
configs:
  settings:
%s    file: ./settings.txt
secrets:
  password:
%s    file: ./password.txt
`, secretValue, configValue, marker, configNameYAML, secretNameYAML)
	require.NoError(t, os.WriteFile(composePath, []byte(manifest), 0o600))
}

func deployFixture(t *testing.T, ctx context.Context, stackDeployer deployer.StackDeployer, stack, composePath string) {
	t.Helper()
	file, err := compose.NewFileLoader().Load(ctx, composePath)
	require.NoError(t, err)
	_, err = stackDeployer.DeployStack(ctx, deployer.StackRequest{
		StackName:         stack,
		SourceComposePath: composePath,
		Desired:           file.Compose,
	})
	require.NoError(t, err)
}

func inspectRealSwarmResources(t *testing.T, ctx context.Context, dockerClient *client.Client, dockerPath, stack, secretName, configName string) (string, string) {
	t.Helper()
	secret, _, err := dockerClient.SecretInspectWithRaw(ctx, secretName)
	require.NoError(t, err)
	config, _, err := dockerClient.ConfigInspectWithRaw(ctx, configName)
	require.NoError(t, err)
	assert.Equal(t, stack, secret.Spec.Labels["com.docker.stack.namespace"])
	assert.Equal(t, stack, config.Spec.Labels["com.docker.stack.namespace"])

	output, err := exec.CommandContext(ctx, dockerPath, "service", "inspect", "--format", "{{json .Spec.TaskTemplate.ContainerSpec}}", stack+"_check").CombinedOutput()
	require.NoError(t, err, "service inspect: %s", output)
	var spec struct {
		Secrets []struct {
			SecretID   string
			SecretName string
		}
		Configs []struct {
			ConfigID   string
			ConfigName string
		}
	}
	require.NoError(t, json.Unmarshal(output, &spec))
	require.Len(t, spec.Secrets, 1)
	require.Len(t, spec.Configs, 1)
	assert.Equal(t, secret.ID, spec.Secrets[0].SecretID)
	assert.Equal(t, secretName, spec.Secrets[0].SecretName)
	assert.Equal(t, config.ID, spec.Configs[0].ConfigID)
	assert.Equal(t, configName, spec.Configs[0].ConfigName)
	return secret.ID, config.ID
}

func waitForRealSwarmTask(t *testing.T, ctx context.Context, dockerPath, serviceName, marker string) {
	t.Helper()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		output, err := exec.CommandContext(ctx, dockerPath, "service", "logs", "--raw", serviceName).CombinedOutput()
		if err == nil && strings.Contains(string(output), marker) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v (logs: %s)", marker, ctx.Err(), output)
		case <-deadline.C:
			ps, _ := exec.CommandContext(ctx, dockerPath, "service", "ps", "--no-trunc", serviceName).CombinedOutput()
			t.Fatalf("Swarm task did not mount resources (%s); service ps: %s; logs: %s", marker, ps, output)
		case <-ticker.C:
		}
	}
}
