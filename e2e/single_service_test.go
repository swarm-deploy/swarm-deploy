package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

func TestRealSwarmSingleServiceIsolationAndFailure(t *testing.T) {
	f := newLifecycleFixture(t, true)
	dir := filepath.Dir(f.path)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password.txt"), []byte("fixture-password"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.txt"), []byte("fixture-settings"), 0o600))
	refs := "    secrets: [password]\n    configs: [settings]\n"
	service := func(name, command string) string {
		return strings.Replace(lifecycleService(name, command, ""), "    deploy:\n", refs+"    deploy:\n", 1)
	}
	objects := "configs:\n  settings:\n    file: ./settings.txt\nsecrets:\n  password:\n    file: ./password.txt\n"
	check := "test -s /run/secrets/password && test -s /settings && sleep 600"
	f.manifest(t, service("job", check)+service("other", check)+objects)
	f.reconcile(t)
	f.runningIDs(t, "job", 1)
	ids := f.runningIDs(t, "other", 1)
	before := f.settled(t, "other")
	selected := f.settled(t, "job")
	baseline := f.latest(t)
	network, err := f.sw.Networks.Get(f.ctx, f.stack+"_default")
	require.NoError(t, err)
	config, err := f.sw.Configs.Get(f.ctx, f.stack+"_settings")
	require.NoError(t, err)
	secret, err := f.sw.Secrets.ResolveReference(f.ctx, f.stack+"_password", "")
	require.NoError(t, err)

	// If the single-service pipeline ever starts Init Jobs this deterministic
	// failing job prevents the otherwise valid application update from succeeding.
	initJob := "    x-init-deploy-jobs:\n      - name: must-not-run\n        image: busybox:1.36\n        command: [sh, -c, 'exit 67']\n"
	changed := strings.Replace(service("job", "echo selected-v2; "+check), "    deploy:\n", initJob+"    deploy:\n", 1)
	desired := f.manifest(t, changed+service("other", "exit 69")+objects)
	for i := range desired.Compose.Services {
		desired.Compose.Services[i].Deploy.Labels = compose.Labels{Map: map[string]string{labelsdict.ServiceManagedLabelKey: "true"}}
	}
	req := deployments.ExecuteServiceRequest{
		StackName: f.stack, SourceComposePath: f.path, ServiceName: "job", Desired: desired.Compose,
	}
	result, err := f.executor.ExecuteService(f.ctx, req)
	require.NoError(t, err, "Init Jobs must not execute and the unselected broken spec must not be applied")
	assert.Empty(t, result.InitJobs)
	assert.True(t, result.Verification.Known)
	assert.Empty(t, result.Verification.UnresolvedServices)
	newIDs := f.runningIDs(t, "job", 1)
	updated := f.settled(t, "job")
	assert.Equal(t, selected.ID, updated.ID)
	assert.Contains(t, updated.Spec.TaskTemplate.ContainerSpec.Args, "echo selected-v2; "+check)
	assert.NotEqual(t, selected.Version, updated.Version)
	assert.NotEmpty(t, newIDs)
	f.unchangedService(t, before, ids)

	// A real failing rolling update still fails verification in the service
	// pipeline, with no stack Deployment/Attempt or collateral service changes.
	req.Desired.Services = append(compose.Services(nil), desired.Compose.Services...)
	for i := range req.Desired.Services {
		if req.Desired.Services[i].Name == "job" {
			req.Desired.Services[i].Command = compose.NewCommand([]string{"sh", "-c", "exit 41"})
		}
	}
	result, err = f.executor.ExecuteService(f.ctx, req)
	require.ErrorIs(t, err, waiter.ErrConvergenceFailed)
	assert.ErrorContains(t, err, "state=failed")
	assert.ErrorContains(t, err, "exit_code=41")
	assert.Empty(t, result.InitJobs)
	assert.True(t, result.Verification.Known)
	assert.Equal(t, []string{"job"}, result.Verification.UnresolvedServices)
	f.unchangedService(t, before, ids)
	assert.Equal(t, baseline.ID, f.latest(t).ID)
	assert.Equal(t, 1, f.latest(t).AttemptCount, "service remediation has no stack lifecycle attempt")
	require.Empty(t, f.publications(t, events.TypeNameServicePruned))
	afterNetwork, err := f.sw.Networks.Get(f.ctx, network.ID)
	require.NoError(t, err)
	assert.Equal(t, network.ID, afterNetwork.ID)
	afterConfig, err := f.sw.Configs.Get(f.ctx, config.ID)
	require.NoError(t, err)
	assert.Equal(t, config.ID, afterConfig.ID)
	afterSecret, err := f.sw.Secrets.ResolveReference(f.ctx, f.stack+"_password", "")
	require.NoError(t, err)
	assert.Equal(t, secret.SecretID, afterSecret.SecretID)
}
