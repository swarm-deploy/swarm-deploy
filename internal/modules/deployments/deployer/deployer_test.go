package deployer

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/converter"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/networks"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestBuildInitJobNameUsesJobNameAndTime(t *testing.T) {
	name := initjob.BuildName("stack-name", "service-name", "Migrations")
	index := strings.LastIndex(name, "-")
	require.Greater(t, index, 0)
	assert.Equal(t, "migrations", name[:index])
	_, err := strconv.ParseInt(name[index+1:], 10, 64)
	require.NoError(t, err)
}

func TestBuildInitJobNameUsesFallbackForEmptyJobName(t *testing.T) {
	assert.True(t, strings.HasPrefix(initjob.BuildName("stack-name", "service-name", ""), "job-"))
}

func TestDeployStackRunsInitJobsBeforeNativeServiceApplication(t *testing.T) {
	events := make([]string, 0, 3)
	jobs := &fakeInitJobExecutor{events: &events}
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: jobs, serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	ctrl := gomock.NewController(t)
	networkManager := swarm.NewMockNetworkManager(ctrl)
	firstGet := networkManager.EXPECT().Get(gomock.Any(), "demo_default").Return(
		swarm.Network{}, swarm.ErrNetworkNotFound,
	)
	networkManager.EXPECT().Create(gomock.Any(), gomock.Any()).After(firstGet).DoAndReturn(
		func(_ context.Context, _ swarm.CreateNetworkRequest) (string, error) {
			events = append(events, "network")
			return "network-id", nil
		})
	existingNetwork := swarm.Network{ID: "network-id", Name: "demo_default", Scope: "swarm", Driver: "overlay",
		Labels: map[string]string{labelsdict.StackNamespace: "demo"}}
	networkManager.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork, nil).Times(2)
	configManager := swarm.NewMockConfigManager(ctrl)
	secretManager := swarm.NewMockSecretManager(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
			events = append(events, "apply")
			return "service-id", nil
		})
	deployer.converter = converter.New(networkManager, configManager, secretManager)
	deployer.networks = networks.New(networkManager)
	deployer.serviceApplier = services.New(manager, &registry.NopAuthManager{})

	desired := minimalCompose("api")
	desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate", Image: "example/migrate:1"}}
	result, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
	require.NoError(t, err)
	assert.Equal(t, []string{"network", "init:api:migrate", "apply"}, events)
	require.Len(t, result.InitJobs, 1)
	require.Len(t, jobs.calls, 1)
	assert.Equal(t, map[string]string{"default": "network-id"}, jobs.calls[0].NetworkTargets)
}

func TestDeployStackInitJobFailureStopsApplicationAndPreservesPartialResult(t *testing.T) {
	jobErr := errors.New("migration failed")
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(),
		initJobRunner:  &fakeInitJobExecutor{errAt: 1, err: jobErr},
		serviceWaiter:  newSuccessfulServiceWaiter(t),
	}
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)
	desired := minimalCompose("api")
	desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate", Image: "example/migrate:1"}}

	result, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "unused.yaml", desired)
	require.ErrorIs(t, err, jobErr)
	assert.True(t, InitJobsFailed(err))
	require.Len(t, result.InitJobs, 1)
	assert.Equal(t, InitJobFailed, result.InitJobs[0].Status)
}

func TestDeployServiceUsesSingleServiceProjectionWithoutInitJobs(t *testing.T) {
	jobs := &fakeInitJobExecutor{}
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: jobs, serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
			assert.Equal(t, "demo_api", spec.Name)
			return "api-id", nil
		})
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)

	desired := compose.Compose{Services: compose.Services{
		{Name: "api", Image: "example/api:1", InitJobs: []compose.InitJob{{Name: "migrate"}}},
		{Name: "worker", Image: "example/worker:1"},
	}}
	result, err := deployer.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
	require.NoError(t, err)
	assert.Empty(t, result.InitJobs)
	assert.Empty(t, jobs.calls)
}

func TestDeployServiceFailsBeforeDockerWritesWhenServiceIsMissing(t *testing.T) {
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{},
		serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)
	_, err := deployer.DeployService(t.Context(), "demo", "source.yaml", "worker", minimalCompose("api"), compose.Compose{})
	require.Error(t, err)
	assert.ErrorContains(t, err, `service "worker" not found`)
}

func TestDeployStackSkipsWaitWhenApplicationIsUnchanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	waiter := NewMockServiceWaitExecutor(ctrl)
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	manager := swarm.NewMockServiceManager(ctrl)
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{
		ID: "service-id", Spec: nativeMinimalServiceSpec("api"),
	}, nil)
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)

	_, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "unused.yaml", minimalCompose("api"))
	require.NoError(t, err)
}

func TestDeployStackParentCancellationInterruptsConvergence(t *testing.T) {
	ctrl := gomock.NewController(t)
	waiter := NewMockServiceWaitExecutor(ctrl)
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).DoAndReturn(
		func(waitCtx context.Context, _ ServiceWaitRequest) error {
			cancel()
			<-waitCtx.Done()
			return waitCtx.Err()
		})
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	wireNativeTestDeployer(t, deployer)

	_, err := deployer.DeployStack(ctx, "demo", "source.yaml", "unused.yaml", minimalCompose("api"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestDeployPathsShareOneApplyDeadline(t *testing.T) {
	for _, testCase := range []struct {
		name string
		run  func(*Deployer) error
	}{
		{name: "stack", run: func(deployer *Deployer) error {
			_, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"))
			return err
		}},
		{name: "single service", run: func(deployer *Deployer) error {
			desired := minimalCompose("api", "worker")
			_, err := deployer.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			waiter := NewMockServiceWaitExecutor(ctrl)
			manager := swarm.NewMockServiceManager(ctrl)
			deadlines := make([]time.Time, 0, 4)
			captureDeadline := func(ctx context.Context) {
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "apply stages must use a bounded context")
				deadlines = append(deadlines, deadline)
			}
			waiter.EXPECT().Snapshot(gomock.Any(), "demo").DoAndReturn(
				func(ctx context.Context, _ string) ([]swarm.StackService, error) {
					captureDeadline(ctx)
					return nil, nil
				},
			)
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").DoAndReturn(
				func(ctx context.Context, _ string) (dockerswarm.Service, error) {
					captureDeadline(ctx)
					return dockerswarm.Service{}, swarm.ErrServiceNotFound
				},
			)
			manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
					captureDeadline(ctx)
					time.Sleep(10 * time.Millisecond)
					return "service-id", nil
				},
			)
			waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ ServiceWaitRequest) error {
					captureDeadline(ctx)
					return nil
				},
			)
			deployer := &Deployer{
				secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
			}
			wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)

			require.NoError(t, testCase.run(deployer))
			require.Len(t, deadlines, 4)
			for _, deadline := range deadlines[1:] {
				assert.True(t, deadlines[0].Equal(deadline), "all apply stages must share one absolute deadline")
			}
		})
	}
}

func TestDeployStackStartsApplyTimeoutAfterInitJobs(t *testing.T) {
	ctrl := gomock.NewController(t)
	jobs := NewMockInitJobExecutor(ctrl)
	jobs.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, spec InitJobSpec) (InitJobResult, error) {
			_, hasDeadline := ctx.Deadline()
			assert.False(t, hasDeadline, "Init Jobs run before the apply timeout starts")
			return InitJobResult{ServiceName: spec.ServiceName, JobName: spec.Job.Name, Status: InitJobSucceeded}, nil
		},
	)
	waiter := NewMockServiceWaitExecutor(ctrl)
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").DoAndReturn(
		func(ctx context.Context, _ string) ([]swarm.StackService, error) {
			_, hasDeadline := ctx.Deadline()
			assert.True(t, hasDeadline, "the apply timeout starts before baseline capture")
			return nil, nil
		},
	)
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(nil)
	deployer := &Deployer{secretResolver: newTestSecretResolver(), initJobRunner: jobs, serviceWaiter: waiter}
	wireNativeTestDeployer(t, deployer)
	desired := minimalCompose("api")
	desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate", Image: "alpine:3"}}

	_, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
	require.NoError(t, err)
}

func TestDeployStackDoesNotReturnBeforeServiceConvergence(t *testing.T) {
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	release := make(chan struct{})
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, ServiceWaitRequest) error {
			<-release
			return nil
		},
	)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	wireNativeTestDeployer(t, deployer)

	result := make(chan error, 1)
	go func() {
		_, err := deployer.DeployStack(context.Background(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"))
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
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	wireNativeTestDeployer(t, deployer)

	_, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"))
	require.ErrorIs(t, err, waitErr)
	assert.True(t, ConvergenceFailed(err))
	assert.ErrorContains(t, err, "wait for stack demo service convergence")
}

func TestDeployServiceWaitsOnlyForTarget(t *testing.T) {
	ctrl := gomock.NewController(t)
	waiter := NewMockServiceWaitExecutor(ctrl)
	baseline := []swarm.StackService{{ID: "api-id", Name: "api"}, {ID: "worker-id", Name: "worker"}}
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(baseline, nil)
	waiter.EXPECT().Wait(gomock.Any(), ServiceWaitRequest{
		StackName: "demo", ServiceNames: []string{"api"}, Baseline: baseline, OnlyAffected: false,
	}).Return(nil)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: waiter,
	}
	wireNativeTestDeployer(t, deployer)
	desired := minimalCompose("api", "worker")

	_, err := deployer.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
	require.NoError(t, err)
}

func TestDeployStackPreservesInitJobResultsWhenNativeApplyFails(t *testing.T) {
	applyErr := errors.New("daemon unavailable")
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("", applyErr)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{},
		serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)
	desired := minimalCompose("api")
	desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate", Image: "alpine:3"}}

	result, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
	require.ErrorIs(t, err, applyErr)
	require.Len(t, result.InitJobs, 1)
	assert.Equal(t, "migrate", result.InitJobs[0].JobName)
}

func TestDeployStackOmitsInitJobResultWhenExecutionNeverStarted(t *testing.T) {
	createErr := errors.New("create init service failed")
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(),
		initJobRunner:  &fakeInitJobExecutor{emptyAt: 1, err: createErr},
		serviceWaiter:  newSuccessfulServiceWaiter(t),
	}
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)
	desired := minimalCompose("api")
	desired.Services[0].InitJobs = []compose.InitJob{{Name: "migrate", Image: "alpine:3"}}

	result, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
	require.ErrorIs(t, err, createErr)
	assert.Empty(t, result.InitJobs)
}

func minimalCompose(names ...string) compose.Compose {
	services := make(compose.Services, 0, len(names))
	for _, name := range names {
		services = append(services, compose.Service{Name: name, Image: "alpine:3"})
	}
	return compose.Compose{Services: services}
}

func nativeMinimalServiceSpec(name string) dockerswarm.ServiceSpec {
	return dockerswarm.ServiceSpec{
		Annotations: dockerswarm.Annotations{Name: "demo_" + name, Labels: map[string]string{
			labelsdict.StackNamespace: "demo", "com.docker.stack.image": "alpine:3",
		}},
		TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Image: "alpine:3", Labels: map[string]string{
				labelsdict.StackNamespace: "demo",
			}},
			Networks: []dockerswarm.NetworkAttachmentConfig{{Target: "network-demo_default", Aliases: []string{name}}},
		},
		Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{}},
		EndpointSpec: &dockerswarm.EndpointSpec{},
	}
}

type fakeInitJobExecutor struct {
	calls   []InitJobSpec
	errAt   int
	emptyAt int
	err     error
	events  *[]string
}

func (e *fakeInitJobExecutor) Run(_ context.Context, spec InitJobSpec) (InitJobResult, error) {
	e.calls = append(e.calls, spec)
	if e.emptyAt > 0 && len(e.calls) == e.emptyAt {
		return InitJobResult{}, e.err
	}
	result := InitJobResult{ServiceName: spec.ServiceName, JobName: spec.Job.Name,
		Status: InitJobSucceeded, StartedAt: time.Now(), FinishedAt: time.Now()}
	if e.events != nil {
		*e.events = append(*e.events, "init:"+spec.ServiceName+":"+spec.Job.Name)
	}
	if e.errAt > 0 && len(e.calls) == e.errAt {
		result.Status = InitJobFailed
		return result, e.err
	}
	return result, nil
}

func newSuccessfulServiceWaiter(t *testing.T) *MockServiceWaitExecutor {
	t.Helper()
	waiter := NewMockServiceWaitExecutor(gomock.NewController(t))
	waiter.EXPECT().Snapshot(gomock.Any(), gomock.Any()).Return([]swarm.StackService{}, nil).AnyTimes()
	waiter.EXPECT().Wait(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return waiter
}
