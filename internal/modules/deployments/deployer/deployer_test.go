package deployer

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
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

func TestDefaultStackDeployArgsUseDetachedMode(t *testing.T) {
	args := defaultStackDeployArgs()
	assert.Contains(t, args, "--detach=true")
	assert.NotContains(t, args, "--detach=false")
}

func TestDeployStackWaitsAfterSuccessfulApply(t *testing.T) {
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	baseline := []swarm.StackService{{ID: "old-api", Name: "api"}}
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(baseline, nil)
	applied := false
	runner := &fakeRunner{onRun: func([]string) error {
		applied = true
		return nil
	}}
	waiter.EXPECT().Wait(gomock.Any(), ServiceWaitRequest{
		StackName: "demo", ServiceNames: []string{"api"}, Baseline: baseline, OnlyAffected: true,
	}).DoAndReturn(func(context.Context, ServiceWaitRequest) error {
		assert.True(t, applied, "waiter must run after the detached apply succeeds")
		return nil
	})
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy", "--detach=true"},
		runner: runner, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}

	_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
		Services: compose.Services{{Name: "api"}},
	})
	require.NoError(t, err)
}

func TestDeployStackDoesNotReturnBeforeServiceConvergence(t *testing.T) {
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	release := make(chan struct{})
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, ServiceWaitRequest) error {
		<-release
		return nil
	})
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy", "--detach=true"},
		runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}

	result := make(chan error, 1)
	go func() {
		_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
			Services: compose.Services{{Name: "api"}},
		})
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("deployment returned before convergence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-result)
}

func TestDeployStackPropagatesServiceWaitFailure(t *testing.T) {
	waitErr := errors.New("service api did not converge")
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(waitErr)
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy", "--detach=true"},
		runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}

	_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
		Services: compose.Services{{Name: "api"}},
	})
	require.ErrorIs(t, err, waitErr)
	assert.ErrorContains(t, err, "wait for stack demo service convergence")
}

func TestDeployServiceWaitsOnlyForTarget(t *testing.T) {
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	baseline := []swarm.StackService{{ID: "api-id", Name: "api"}, {ID: "worker-id", Name: "worker"}}
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(baseline, nil)
	waiter.EXPECT().Wait(gomock.Any(), ServiceWaitRequest{
		StackName: "demo", ServiceNames: []string{"api"}, Baseline: baseline, OnlyAffected: false,
	}).Return(nil)
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy", "--detach=true"},
		runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	desired := compose.Compose{Services: compose.Services{{Name: "api"}, {Name: "worker"}}}

	_, err := d.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
	require.NoError(t, err)
}

func TestDeployStackRunsInitJobsBeforeDeploy(t *testing.T) {
	events := make([]string, 0, 4)
	initJobs := &fakeInitJobExecutor{
		events: &events,
	}
	runner := &fakeRunner{
		events: &events,
	}
	deployer := &Deployer{secretResolver: newTestSecretResolver(),
		stackDeployArgs: []string{"stack", "deploy", "--prune"},
		runner:          runner,
		initJobRunner:   initJobs,
		serviceWaiter:   newSuccessfulServiceWaiter(t),
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
	result, err := deployer.DeployStack(
		context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services},
	)
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
	require.Len(t, result.InitJobs, 3, "all Init Job results must be returned")
	assert.Equal(t, []string{"migrate", "seed", "warm-cache"}, []string{
		result.InitJobs[0].JobName, result.InitJobs[1].JobName, result.InitJobs[2].JobName,
	})
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
	deployer := &Deployer{secretResolver: newTestSecretResolver(),
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		initJobRunner:   initJobs,
		serviceWaiter:   newSuccessfulServiceWaiter(t),
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

	result, err := deployer.DeployStack(
		context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services},
	)
	require.Error(t, err, "deploy stack must fail when init job fails")
	assert.ErrorContains(t, err, "service api init job seed", "error must include failed init job details")
	assert.ErrorIs(t, err, initErr, "error must keep original init failure")

	require.Len(t, initJobs.calls, 2, "execution must stop at first failed init job")
	require.Len(t, result.InitJobs, 2, "the partial result must include the failed execution")
	assert.Equal(t, InitJobFailed, result.InitJobs[1].Status)
	require.Empty(t, runner.calls, "deploy command must not run after init job failure")
}

func TestDeployStackDeploysWithoutInitJobs(t *testing.T) {
	initJobs := &fakeInitJobExecutor{}
	runner := &fakeRunner{}
	deployer := &Deployer{secretResolver: newTestSecretResolver(),
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          runner,
		initJobRunner:   initJobs,
		serviceWaiter:   newSuccessfulServiceWaiter(t),
	}

	services := []compose.Service{
		{Name: "api"},
		{Name: "worker"},
	}

	result, err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{Services: services})
	require.NoError(t, err, "deploy stack")
	assert.Empty(t, result.InitJobs)

	require.Empty(t, initJobs.calls, "init jobs should not run when there are no definitions")
	require.Len(t, runner.calls, 1, "deploy command must still be executed")
	assert.Equal(
		t,
		[]string{"stack", "deploy", "-c", "/tmp/demo.yaml", "demo"},
		runner.calls[0],
		"unexpected deploy command arguments",
	)
}

func TestDeployStackReturnsInitJobResultsWhenDeployCommandFails(t *testing.T) {
	deployErr := errors.New("deploy failed")
	deployer := &Deployer{secretResolver: newTestSecretResolver(),
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          &fakeRunner{err: deployErr},
		initJobRunner:   &fakeInitJobExecutor{},
		serviceWaiter:   newSuccessfulServiceWaiter(t),
	}

	result, err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{
		Services: []compose.Service{{
			Name:     "api",
			InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
		}},
	})

	require.ErrorIs(t, err, deployErr)
	require.Len(t, result.InitJobs, 1)
	assert.Equal(t, "migrate", result.InitJobs[0].JobName)
}

func TestDeployStackOmitsInitJobResultWhenExecutionNeverStarted(t *testing.T) {
	createErr := errors.New("create service failed")
	deployer := &Deployer{secretResolver: newTestSecretResolver(),
		stackDeployArgs: []string{"stack", "deploy"},
		runner:          &fakeRunner{},
		initJobRunner:   &fakeInitJobExecutor{emptyAt: 1, err: createErr},
		serviceWaiter:   newSuccessfulServiceWaiter(t),
	}

	result, err := deployer.DeployStack(context.Background(), "demo", "/tmp/source.yaml", "/tmp/demo.yaml", compose.Compose{
		Services: []compose.Service{{
			Name:     "api",
			InitJobs: []compose.InitJob{{Name: "migrate", Image: "example/migrate:latest"}},
		}},
	})

	require.ErrorIs(t, err, createErr)
	assert.Empty(t, result.InitJobs)
}

func TestDeployServiceDeploysOnlyTargetService(t *testing.T) {
	var renderedPath string
	runner := &fakeRunner{onRun: func(args []string) error {
		renderedPath = composePathFromDeployArgs(args)
		payload, err := os.ReadFile(renderedPath)
		require.NoError(t, err)
		assert.Contains(t, string(payload), "api:")
		assert.NotContains(t, string(payload), "worker:")
		assert.Contains(t, string(payload), "backend:")
		assert.NotContains(t, string(payload), "frontend:")
		assert.Contains(t, string(payload), "api-data:")
		assert.NotContains(t, string(payload), "worker-data:")
		return nil
	}}
	initJobs := &fakeInitJobExecutor{}
	d := &Deployer{secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
		runner: runner, initJobRunner: initJobs, serviceWaiter: newSuccessfulServiceWaiter(t)}

	desired := compose.Compose{
		Services: compose.Services{
			{
				Name: "api", Image: "example/api:1", InitJobs: []compose.InitJob{{Name: "migrate"}},
				Networks: compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "backend"}),
				Volumes: compose.ServiceVolumes{Volumes: []*compose.ServiceVolume{{
					Type: compose.ServiceVolumeTypeVolume, Source: "api-data", Target: "/data",
				}}},
			},
			{Name: "worker", Image: "example/worker:1"},
		},
		Networks: map[string]compose.Network{"backend": {}, "frontend": {}},
		Volumes: compose.Volumes{
			"api-data": {}, "worker-data": {},
		},
	}
	deploy := desired
	deploy.Services = append(compose.Services(nil), desired.Services...)
	deploy.Services[0].InitJobs = nil
	result, err := d.DeployService(context.Background(), "demo", "/tmp/source.yaml", "api", desired, deploy)
	require.NoError(t, err)
	assert.Empty(t, result.InitJobs)
	assert.Empty(t, initJobs.calls)
	require.Len(t, runner.calls, 1)
	assert.NotEmpty(t, renderedPath)
	_, err = os.Stat(renderedPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestSingleServiceComposeKeepsOnlyReferencedObjects(t *testing.T) {
	desired := compose.Compose{
		Services: compose.Services{{
			Name:     "api",
			Secrets:  []compose.ObjectRef{{Source: "api-secret"}},
			Configs:  []compose.ObjectRef{{Source: "api-config"}},
			Networks: compose.NewServiceNetworks(&compose.ServiceNetwork{Alias: "backend"}),
		}},
		Networks: map[string]compose.Network{"backend": {}, "frontend": {}},
		Secrets: compose.Secrets{
			"api-secret": {Alias: "api-secret"}, "worker-secret": {Alias: "worker-secret"},
		},
		Configs: compose.Configs{
			"api-config": {Alias: "api-config"}, "worker-config": {Alias: "worker-config"},
		},
	}

	selected, err := singleServiceCompose(desired, "api")
	require.NoError(t, err)
	assert.Equal(t, map[string]compose.Network{"backend": {}}, selected.Networks)
	assert.Equal(t, compose.Secrets{"api-secret": desired.Secrets["api-secret"]}, selected.Secrets)
	assert.Equal(t, compose.Configs{"api-config": desired.Configs["api-config"]}, selected.Configs)
}

func TestSingleServiceComposeSelectsServiceNetworks(t *testing.T) {
	internal := true
	tests := []struct {
		name     string
		service  compose.Service
		networks map[string]compose.Network
		want     map[string]compose.Network
	}{
		{
			name:    "implicit external default network",
			service: compose.Service{Name: "api"},
			networks: map[string]compose.Network{
				"default": {Name: "shared-network", External: true},
			},
			want: map[string]compose.Network{"default": {Name: "shared-network", External: true}},
		},
		{
			name:    "implicit custom default network",
			service: compose.Service{Name: "api"},
			networks: map[string]compose.Network{
				"default": {Internal: &internal, Extra: map[string]interface{}{"driver": "overlay"}},
			},
			want: map[string]compose.Network{
				"default": {Internal: &internal, Extra: map[string]interface{}{"driver": "overlay"}},
			},
		},
		{
			name: "explicitly referenced network",
			service: compose.Service{Name: "api", Networks: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
			)},
			networks: map[string]compose.Network{
				"backend": {Name: "shared-backend", External: true},
			},
			want: map[string]compose.Network{"backend": {Name: "shared-backend", External: true}},
		},
		{
			name: "unrelated network definitions are omitted",
			service: compose.Service{Name: "api", Networks: compose.NewServiceNetworks(
				&compose.ServiceNetwork{Alias: "backend"},
			)},
			networks: map[string]compose.Network{
				"backend": {}, "frontend": {}, "default": {Name: "unused-default"},
			},
			want: map[string]compose.Network{"backend": {}},
		},
		{
			name:     "service without network declarations and no configured default",
			service:  compose.Service{Name: "api"},
			networks: map[string]compose.Network{"frontend": {}},
		},
		{
			name:    "compose without network definitions",
			service: compose.Service{Name: "api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := singleServiceCompose(compose.Compose{
				Services: compose.Services{tt.service}, Networks: tt.networks,
			}, "api")
			require.NoError(t, err)
			assert.Equal(t, tt.want, selected.Networks)
		})
	}
}

func TestDeployServiceFailsWhenServiceIsMissing(t *testing.T) {
	d := &Deployer{secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
		runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: newSuccessfulServiceWaiter(t)}

	desired := compose.Compose{Services: compose.Services{{Name: "api"}}}
	_, err := d.DeployService(context.Background(), "demo", "/tmp/source.yaml", "worker", desired, desired)
	require.Error(t, err)
	assert.ErrorContains(t, err, "service \"worker\" not found")
}

type fakeRunner struct {
	calls  [][]string
	events *[]string
	err    error
	onRun  func([]string) error
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

	return "", r.err
}

func composePathFromDeployArgs(args []string) string {
	for index := 0; index < len(args)-1; index++ {
		if args[index] == "-c" {
			return args[index+1]
		}
	}
	return ""
}

type fakeInitJobExecutor struct {
	calls   []InitJobSpec
	errAt   int
	emptyAt int
	err     error
	events  *[]string
}

func (e *fakeInitJobExecutor) Run(
	_ context.Context,
	spec InitJobSpec,
) (InitJobResult, error) {
	e.calls = append(e.calls, spec)
	if e.emptyAt > 0 && len(e.calls) == e.emptyAt {
		return InitJobResult{}, e.err
	}
	execution := InitJobResult{
		ServiceName: spec.ServiceName, JobName: spec.Job.Name,
		Status: InitJobSucceeded, StartedAt: time.Now(), FinishedAt: time.Now(),
	}

	if e.events != nil {
		*e.events = append(*e.events, "init:"+spec.ServiceName+":"+spec.Job.Name)
	}

	if e.errAt > 0 && len(e.calls) == e.errAt {
		execution.Status = InitJobFailed
		return execution, e.err
	}

	return execution, nil
}

func newSuccessfulServiceWaiter(t *testing.T) *MockServiceWaitExecutor {
	t.Helper()
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	waiter.EXPECT().Snapshot(gomock.Any(), gomock.Any()).Return([]swarm.StackService{}, nil).AnyTimes()
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return waiter
}
