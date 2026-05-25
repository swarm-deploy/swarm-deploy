package deployer

import (
	"context"
	"errors"
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

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/demo.yaml", services)
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

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/demo.yaml", services)
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

	err := deployer.DeployStack(context.Background(), "demo", "/tmp/demo.yaml", services)
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

type fakeRunner struct {
	calls  [][]string
	events *[]string
}

func (r *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	copiedArgs := append([]string(nil), args...)
	r.calls = append(r.calls, copiedArgs)

	if r.events != nil {
		*r.events = append(*r.events, "deploy")
	}

	return "", nil
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
