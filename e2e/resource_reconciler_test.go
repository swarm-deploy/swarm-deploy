package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// TestDeployStackOnRealSwarm exercises the actual Docker Engine, Docker CLI, and a
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
	preflightLog := filepath.Join(dir, "preflight.log")
	wrapperPath := filepath.Join(dir, "docker-wrapper.sh")
	// The wrapper inspects real Swarm resources BEFORE it invokes docker stack deploy.
	// A regression that lets Docker CLI create resources instead of the reconciler
	// must fail here, rather than accidentally pass after Docker CLI creates them.
	wrapper := `#!/bin/sh
set -eu
if [ "$1" = "stack" ] && [ "$2" = "deploy" ]; then
  "$SWARM_E2E_REAL_DOCKER" secret inspect "$SWARM_E2E_SECRET_NAME" >/dev/null
  "$SWARM_E2E_REAL_DOCKER" config inspect "$SWARM_E2E_CONFIG_NAME" >/dev/null
  printf '%s %s\n' "$SWARM_E2E_SECRET_NAME" "$SWARM_E2E_CONFIG_NAME" >> "$SWARM_E2E_PREFLIGHT_LOG"
fi
exec "$SWARM_E2E_REAL_DOCKER" "$@"
`
	require.NoError(t, os.WriteFile(wrapperPath, []byte(wrapper), 0o700))
	absDockerPath, err := filepath.Abs(dockerPath)
	require.NoError(t, err)
	t.Setenv("SWARM_E2E_REAL_DOCKER", absDockerPath)
	t.Setenv("SWARM_E2E_PREFLIGHT_LOG", preflightLog)
	t.Setenv("SWARM_E2E_SECRET_NAME", stack+"_password")
	t.Setenv("SWARM_E2E_CONFIG_NAME", stack+"_settings")

	swarmService := swarm.NewSwarm(dockerClient, wrapperPath)
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

	// Deploying the same stack again must reuse IDs and keep the task healthy.
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	secretIDAgain, configIDAgain := inspectRealSwarmResources(t, ctx, dockerClient, dockerPath, stack, stack+"_password", stack+"_settings")
	assert.Equal(t, secretID1, secretIDAgain, "unchanged secret must be reused")
	assert.Equal(t, configID1, configIDAgain, "unchanged config must be reused")

	// Simulate the names produced by hash-based rotation and verify that
	// new resource versions are created and mounted in the new task.
	rotatedSecretName := stack + "-password-v2"
	rotatedConfigName := stack + "-settings-v2"
	t.Setenv("SWARM_E2E_SECRET_NAME", rotatedSecretName)
	t.Setenv("SWARM_E2E_CONFIG_NAME", rotatedConfigName)
	writeRealSwarmFixture(t, dir, composePath, rotatedSecretName, rotatedConfigName, "secret-v2", "config-v2", "mounted-v2")
	deployFixture(t, ctx, stackDeployer, stack, composePath)
	secretID2, configID2 := inspectRealSwarmResources(t, ctx, dockerClient, dockerPath, stack, rotatedSecretName, rotatedConfigName)
	assert.NotEqual(t, secretID1, secretID2, "rotated secret must be a different Docker object")
	assert.NotEqual(t, configID1, configID2, "rotated config must be a different Docker object")
	waitForRealSwarmTask(t, ctx, dockerPath, stack+"_check", "mounted-v2")
	preflights, err := os.ReadFile(preflightLog)
	require.NoError(t, err)
	assert.Equal(t, []string{
		stack + "_password " + stack + "_settings",
		stack + "_password " + stack + "_settings",
		rotatedSecretName + " " + rotatedConfigName,
	}, strings.Split(strings.TrimSpace(string(preflights)), "\n"),
		"all three deployments must have resources ready before Docker CLI starts")
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
	_, err = stackDeployer.DeployStack(ctx, stack, composePath, composePath, file.Compose)
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
