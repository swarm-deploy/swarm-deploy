package deployer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func TestBuildInitJobNameUsesJobNameAndTime(t *testing.T) {
	name := buildInitJobName("stack-name", "service-name", "Migrations")

	idx := strings.LastIndex(name, "-")
	require.Greater(t, idx, 0, "job name must contain timestamp suffix")

	assert.Equal(t, "migrations", name[:idx], "job name prefix must be sanitized job name")

	_, err := strconv.ParseInt(name[idx+1:], 10, 64)
	require.NoError(t, err, "job name suffix must be unix timestamp")
}

func TestBuildInitJobNameUsesFallbackForEmptyJobName(t *testing.T) {
	name := buildInitJobName("stack-name", "service-name", "")

	assert.True(t, strings.HasPrefix(name, "job-"), "empty job name must fallback to job prefix")
}

func TestDeployStackRunsInitJobsBeforeDeploy(t *testing.T) {
	events := make([]string, 0, 4)
	initJobs := &fakeInitJobExecutor{
		events: &events,
	}
	runner := &fakeRunner{
		events: &events,
	}
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy", "--prune"},
		runner:          runner,
		initJobRunner:   initJobs,
	}

	services := []compose.Service{
		{
			Name: "api",
			Networks: compose.NewServiceNetworks(&compose.ServiceNetwork{
				ResolvedName: "default",
			}),
			Secrets: []compose.ObjectRef{
				{Source: "db-password"},
			},
			Configs: []compose.ObjectRef{
				{Source: "api-config"},
			},
			InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "example/migrate:latest"},
				{Name: "seed", Image: "example/seed:latest"},
			},
		},
		{
			Name: "worker",
			InitJobs: []compose.InitJob{
				{Name: "warm-cache", Image: "example/warm-cache:latest"},
			},
		},
	}

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services})
	require.NoError(t, err, "deploy stack")

	assert.Equal(
		t,
		[]string{
			"init:api:migrate",
			"init:api:seed",
			"init:worker:warm-cache",
			"deploy",
		},
		events,
		"init jobs must complete before deploy command",
	)

	require.Len(t, initJobs.calls, 3, "unexpected init job calls count")
	assert.Equal(t, "api", initJobs.calls[0].ServiceName, "first job must belong to first service")
	assert.Equal(t, "migrate", initJobs.calls[0].Job.Name, "unexpected first init job")
	assert.Equal(t, []string{"default"}, initJobs.calls[0].DefaultNetwork, "service networks must be passed to init job")
	assert.Equal(t, []compose.ObjectRef{{Source: "db-password"}},
		initJobs.calls[0].ServiceSecrets, "service secrets must be passed to init job")
	assert.Equal(t, []compose.ObjectRef{{Source: "api-config"}},
		initJobs.calls[0].ServiceConfigs, "service configs must be passed to init job")

	require.Len(t, runner.calls, 1, "deploy command must be called once")
	assert.Equal(
		t,
		[]string{"stack", "deploy", "--prune", "-c", "/tmp/demo.yaml", "demo"},
		runner.calls[0],
		"unexpected deploy command arguments",
	)
}

func TestDeployStackStopsWhenInitJobFails(t *testing.T) {
	initErr := errors.New("init failed")
	initJobs := &fakeInitJobExecutor{
		errAt: 2,
		err:   initErr,
	}
	runner := &fakeRunner{}
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		initJobRunner:   initJobs,
	}

	services := []compose.Service{
		{
			Name: "api",
			InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "example/migrate:latest"},
				{Name: "seed", Image: "example/seed:latest"},
			},
		},
		{
			Name: "worker",
			InitJobs: []compose.InitJob{
				{Name: "warm-cache", Image: "example/warm-cache:latest"},
			},
		},
	}

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services})
	require.Error(t, err, "deploy stack must fail when init job fails")
	assert.ErrorContains(t, err, "service api init job seed", "error must include failed init job details")
	assert.ErrorIs(t, err, initErr, "error must keep original init failure")

	require.Len(t, initJobs.calls, 2, "execution must stop at first failed init job")
	require.Empty(t, runner.calls, "deploy command must not run after init job failure")
}

func TestDeployStackDeploysWithoutInitJobs(t *testing.T) {
	initJobs := &fakeInitJobExecutor{}
	runner := &fakeRunner{}
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		initJobRunner:   initJobs,
	}

	services := []compose.Service{
		{Name: "api"},
		{Name: "worker"},
	}

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services})
	require.NoError(t, err, "deploy stack")

	require.Empty(t, initJobs.calls, "init jobs should not run when there are no definitions")
	require.Len(t, runner.calls, 1, "deploy command must still be executed")
	assert.Equal(
		t,
		[]string{"stack", "deploy", "-c", "/tmp/demo.yaml", "demo"},
		runner.calls[0],
		"unexpected deploy command arguments",
	)
}

func TestDeployServiceDeploysSingleServiceCompose(t *testing.T) {
	dir := t.TempDir()
	sourceComposePath := filepath.Join(dir, "stack.yaml")
	sourceCompose := []byte(`
version: "3.9"
services:
  api:
    image: ghcr.io/example/api:1.0.0
    networks:
      - backend
    secrets:
      - source: app_secret
        target: app_secret
    x-init-deploy-jobs:
      - name: migrate
        image: ghcr.io/example/migrate:1.0.0
        command: ["migrate", "up"]
  worker:
    image: ghcr.io/example/worker:1.0.0
networks:
  backend: {}
secrets:
  app_secret:
    external: true
volumes:
  data: {}
`)
	require.NoError(t, os.WriteFile(sourceComposePath, sourceCompose, 0o600), "write source compose")

	stackFile, err := compose.Load(sourceComposePath)
	require.NoError(t, err, "load source compose")
	require.Len(t, stackFile.Services, 2, "unexpected parsed services count")

	var renderedPath string
	runner := &fakeRunner{
		onRun: func(args []string) error {
			renderedPath = composePathFromDeployArgs(args)
			require.NotEmpty(t, renderedPath, "compose path must be present in deploy args")

			renderedPayload, readErr := os.ReadFile(renderedPath)
			require.NoError(t, readErr, "read rendered compose")

			rendered := string(renderedPayload)
			assert.Contains(t, rendered, "services:", "rendered compose must include services section")
			assert.Contains(t, rendered, "api:", "target service must be present")
			assert.NotContains(t, rendered, "worker:", "non-target service must be excluded")
			assert.Contains(t, rendered, "networks:", "rendered compose must include top-level networks")
			assert.Contains(t, rendered, "secrets:", "rendered compose must include top-level secrets")
			assert.Contains(t, rendered, "volumes:", "rendered compose must include top-level volumes")

			return nil
		},
	}
	initJobs := &fakeInitJobExecutor{}
	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		initJobRunner:   initJobs,
	}

	err = deployer.DeployService(context.Background(), "demo", sourceComposePath, stackFile.Services[0])
	require.NoError(t, err, "deploy service")

	require.Len(t, initJobs.calls, 1, "only target service init jobs must run")
	assert.Equal(t, "api", initJobs.calls[0].ServiceName, "unexpected init job service name")
	require.Len(t, runner.calls, 1, "deploy command must be called once")
	assert.Equal(t, "demo", runner.calls[0][len(runner.calls[0])-1], "stack name must be preserved")
	assert.NotEmpty(t, renderedPath, "rendered path must be captured")
	_, statErr := os.Stat(renderedPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "temporary compose file must be removed after deploy")
}

func TestDeployServiceFailsWhenServiceNotFoundInCompose(t *testing.T) {
	dir := t.TempDir()
	sourceComposePath := filepath.Join(dir, "stack.yaml")
	sourceCompose := []byte(`
services:
  api:
    image: ghcr.io/example/api:1.0.0
`)
	require.NoError(t, os.WriteFile(sourceComposePath, sourceCompose, 0o600), "write source compose")

	deployer := &Deployer{
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          &fakeRunner{},
		initJobRunner:   &fakeInitJobExecutor{},
	}

	err := deployer.DeployService(context.Background(), "demo", sourceComposePath, compose.Service{
		Name: "worker",
	})
	require.Error(t, err, "expected missing service error")
	assert.ErrorContains(t, err, "service \"worker\" not found", "unexpected error")
}

type fakeRunner struct {
	calls  [][]string
	events *[]string
	onRun  func(args []string) error
	err    error
}

func (r *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	copiedArgs := append([]string(nil), args...)
	r.calls = append(r.calls, copiedArgs)

	if r.events != nil {
		*r.events = append(*r.events, "deploy")
	}

	if r.onRun != nil {
		if err := r.onRun(copiedArgs); err != nil {
			return "", err
		}
	}
	if r.err != nil {
		return "", r.err
	}

	return "", nil
}

func composePathFromDeployArgs(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c" {
			return args[i+1]
		}
	}
	return ""
}

type fakeInitJobExecutor struct {
	calls  []InitJobSpec
	errAt  int
	err    error
	events *[]string
}

func (e *fakeInitJobExecutor) Run(_ context.Context, spec InitJobSpec) error {
	e.calls = append(e.calls, spec)

	if e.events != nil {
		*e.events = append(*e.events, "init:"+spec.ServiceName+":"+spec.Job.Name)
	}

	if e.errAt > 0 && len(e.calls) == e.errAt {
		return e.err
	}

	return nil
}
