package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestRealSwarmPausedUpdateRetryAndRecovery(t *testing.T) {
	if os.Getenv("SWARM_DEPLOY_E2E") != "1" {
		t.Skip("set SWARM_DEPLOY_E2E=1 to run against a real initialized Docker Swarm")
	}
	dockerPath, err := exec.LookPath("docker")
	require.NoError(t, err)
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dockerClient.Close()) })
	stack := fmt.Sprintf("sdconverge%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, dockerPath, "stack", "rm", stack).CombinedOutput()
		assert.NoError(t, err, "cleanup: %s", output)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	sw := swarm.NewSwarm(dockerClient)
	bus := &dispatcher.NopDispatcher{}
	lifecycle := deployments.NewService(db, bus)
	stackDeployer := deployer.NewDeployer(100*time.Millisecond, time.Minute, dockerClient, sw, nil, deployer.NewSecretResolver(fs.NewLocalFileSystem(), deployer.PlaintextDecoder{}))
	controller := deployments.NewController(lifecycle, stackDeployer, sw.Services, pruner.NewServicePruner(sw.Services, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
	sideVersion := "v1"
	load := func(command string) compose.File {
		path := filepath.Join(dir, "compose.yaml")
		manifest := fmt.Sprintf(`services:
  api:
    image: busybox:1.36
    command: ["sh", "-c", %q]
    deploy:
      replicas: 2
      restart_policy:
        condition: none
      update_config:
        order: start-first
        failure_action: pause
        monitor: 1s
  other:
    image: busybox:1.36
    command: ["sleep", "600"]
    environment:
      VERSION: %q
`, command, sideVersion)
		require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
		file, err := compose.NewFileLoader().Load(ctx, path)
		require.NoError(t, err)
		return *file
	}
	req := deployments.ExecuteRequest{Stack: config.StackSpec{Name: stack}, Commit: "healthy", Desired: load("sleep 600"), SourceChanged: true}
	result, err := controller.Execute(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, result.Status)
	req.Commit, req.Desired = "failed", load("exit 1")
	result, err = controller.Execute(ctx, req)
	require.Error(t, err)
	require.NotNil(t, result.Attempt)
	assert.Equal(t, deploymentmodel.Failed, result.Status)
	assert.Equal(t, "convergence_failed", result.ErrorCode)
	stored, err := deploymentstore.NewStore(db).Get(ctx, result.Attempt.DeploymentID)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.PhaseVerification, stored.Phase)
	assert.Contains(t, stored.ErrorDiagnostic, "exit_code=1")
	service, err := sw.Services.Inspect(ctx, stack+"_api")
	require.NoError(t, err)
	require.NotNil(t, service.UpdateStatus)
	assert.Equal(t, "paused", string(service.UpdateStatus.State))
	require.NotNil(t, service.UpdateStatus.StartedAt)
	tasks, err := sw.Services.ListTasks(ctx, service.ID)
	require.NoError(t, err)
	oldRunning, failedNew := false, false
	for _, task := range tasks {
		if task.CurrentState == swarm.TaskStateRunning && task.CreatedAt.Before(*service.UpdateStatus.StartedAt) {
			oldRunning = true
		}
		if task.CurrentState == swarm.TaskStateFailed && !task.CreatedAt.Before(*service.UpdateStatus.StartedAt) {
			failedNew = true
		}
	}
	require.True(t, oldRunning, "a paused rollout retains an old Running task")
	require.True(t, failedNew, "the failed task belongs to the new generation")
	for range 3 {
		skipped, err := controller.Execute(ctx, req)
		require.NoError(t, err)
		assert.Nil(t, skipped.Attempt)
		assert.True(t, skipped.RetryDeferred)
	}

	// A change to the other service must not hide api's unresolved failed rollout.
	sideVersion = "v2"
	req.Commit, req.Desired = "other-changed", load("exit 1")
	changed, err := controller.Execute(ctx, req)
	require.Error(t, err)
	require.NotNil(t, changed.Attempt)
	assert.NotEqual(t, result.Attempt.DeploymentID, changed.Attempt.DeploymentID)
	assert.Equal(t, deploymentmodel.Failed, changed.Status)
	assert.Equal(t, "convergence_failed", changed.ErrorCode)
	unchangedAPI, err := sw.Services.Inspect(ctx, service.ID)
	require.NoError(t, err)
	assert.Equal(t, service.Spec.TaskTemplate.ForceUpdate, unchangedAPI.Spec.TaskTemplate.ForceUpdate)
	pending, _, err := deploymentstore.NewStore(db).Latest(ctx, stack)
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, pending.Verification.UnresolvedServices, "the other service was verified even while api failed")
	for range 3 {
		skipped, executeErr := controller.Execute(ctx, req)
		require.NoError(t, executeErr)
		assert.True(t, skipped.RetryDeferred)
		assert.Nil(t, skipped.Attempt)
	}
	req.ManualRetry = true
	retried, err := controller.Execute(ctx, req)
	require.Error(t, err, "the command still exits 1; retry must perform a real update and fail again")
	require.NotNil(t, retried.Attempt)
	assert.Equal(t, 2, retried.Attempt.Number)
	assert.Equal(t, "convergence_failed", retried.ErrorCode)
	after, err := sw.Services.Inspect(ctx, service.ID)
	require.NoError(t, err)
	assert.Equal(t, service.Spec.TaskTemplate.ForceUpdate+1, after.Spec.TaskTemplate.ForceUpdate)
	req.ManualRetry = false
	req.Commit, req.Desired = "recovered", load("sleep 600")
	recovered, err := controller.Execute(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, recovered.Status)
}
