package deployer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"

	pipe "github.com/artarts36/gopipe"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

func TestDeployPipelinesPreserveRequiredPhaseOrdering(t *testing.T) {
	recorder := setupPipelineTracing(t)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{},
		serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	wireNativeTestDeployer(t, deployer)
	require.NoError(t, runMinimalStack(t, deployer))

	spans := recorder.Ended()
	wantStages := []string{
		stepValidateDesired, stepReconcileNetworks, stepReconcileResources, stepConvertStack,
		stepRunInitJobs, stepCaptureBaseline, stepApplyStack, stepWaitConvergence,
	}
	var previous sdktrace.ReadOnlySpan
	for _, name := range wantStages {
		stage := requireSpan(t, spans, name)
		if previous != nil {
			assert.False(t, stage.StartTime().Before(previous.StartTime()), "%s stage order", name)
		}
		previous = stage
	}
	root := requireSpan(t, spans, deployStackOperationName)
	assert.Equal(t, codes.Ok, root.Status().Code)
	assertSpanAttributes(t, root, map[attribute.Key]string{
		tracing.ResourceStackName: "demo", tracing.ResourceComposePath: "rendered.yaml",
	})
}

func TestDeployPipelineFailureSetsSafeRootSpanError(t *testing.T) {
	recorder := setupPipelineTracing(t)
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		"", errors.New("registry failed: token=private-value"),
	)
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{},
		serviceWaiter: newSuccessfulServiceWaiter(t),
	}
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)

	err := runMinimalStack(t, deployer)
	require.Error(t, err)
	root := requireSpan(t, recorder.Ended(), deployStackOperationName)
	assert.Equal(t, codes.Error, root.Status().Code)
	assert.NotContains(t, fmt.Sprint(root.Status(), root.Events()), "private-value")
	assert.Contains(t, fmt.Sprint(root.Status(), root.Events()), "token=****")
}

func TestDeployStackStepErrorPreservesJobFailureType(t *testing.T) {
	jobErr := &JobFailedError{ID: "task-id", Name: "migrate", Reason: "exit code 1"}
	deployer := &Deployer{
		secretResolver: newTestSecretResolver(),
		initJobRunner:  &fakeInitJobExecutor{errAt: 1, err: jobErr},
		serviceWaiter:  newSuccessfulServiceWaiter(t),
	}
	wireNativeTestDeployer(t, deployer)
	desired := minimalCompose("api")
	desired.Services[0].InitJobs = append(desired.Services[0].InitJobs, composeInitJob("migrate"))

	result, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
	require.Error(t, err)
	require.Len(t, result.InitJobs, 1)
	assert.ErrorIs(t, err, jobErr)
	var stepErr *pipe.StepError
	require.True(t, errors.As(err, &stepErr))
	assert.Equal(t, stepRunInitJobs, stepErr.StepName)
}

func TestPipelineErrorIsTraceSafeAndPreservesCause(t *testing.T) {
	cause := errors.New("registry failed: token=private-value")
	err := safePipelineError(cause)
	assert.ErrorIs(t, err, cause)
	assert.NotContains(t, err.Error(), "private-value")
	assert.Contains(t, err.Error(), "token=****")
}

func TestDeployPipelinesShareInterpolatedDesiredState(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "3")
	t.Setenv("SD_TEST_TEXT", "$$HOME ${SD_TEST_TAG}")
	loader := compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
		return []byte(`services:
  api:
    image: 'alpine:${SD_TEST_TAG}'
    command: ['sh', '-c', 'echo $$HOME $$(date) $$$$ ${SD_TEST_TEXT}']
    entrypoint: ['/bin/${SD_TEST_SHELL:-sh}']
    environment: {ESCAPED: '$$HOME', INSERTED: '${SD_TEST_TEXT}'}
    healthcheck: {test: [CMD-SHELL, 'test -n "$$HOME"']}
`), nil
	})
	file, err := loader.Load(t.Context(), "source.yaml")
	require.NoError(t, err)
	// Persisted snapshots contain effective values. Remediation must not expand
	// them again, even if the process environment changed in the meantime.
	payload, err := json.Marshal(file.Compose)
	require.NoError(t, err)
	var restored compose.Compose
	require.NoError(t, json.Unmarshal(payload, &restored))
	t.Setenv("SD_TEST_TAG", "changed")
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound).Times(2)
	var applied []dockerswarm.ServiceSpec
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceCreateOptions) (string, error) {
			applied = append(applied, spec)
			return "api-id", nil
		}).Times(2)
	deployer := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: newSuccessfulServiceWaiter(t)}
	wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)
	_, err = deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", file.Compose)
	require.NoError(t, err)
	_, err = deployer.DeployService(t.Context(), "demo", "source.yaml", "api", restored, restored)
	require.NoError(t, err)
	require.Len(t, applied, 2)
	assert.Equal(t, applied[0], applied[1])
	container := applied[0].TaskTemplate.ContainerSpec
	assert.Equal(t, "alpine:3", container.Image)
	assert.Equal(t, []string{"sh", "-c", "echo $HOME $(date) $$ $$HOME ${SD_TEST_TAG}"}, container.Args)
	assert.ElementsMatch(t, []string{"ESCAPED=$HOME", "INSERTED=$$HOME ${SD_TEST_TAG}"}, container.Env)
}

func runMinimalStack(t *testing.T, deployer *Deployer) error {
	t.Helper()
	_, err := deployer.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"))
	return err
}

func composeInitJob(name string) compose.InitJob {
	return compose.InitJob{Name: name, Image: "alpine:3"}
}

func setupPipelineTracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	return recorder
}

func requireSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	require.FailNow(t, "span not found", name)
	return nil
}

func assertSpanAttributes(t *testing.T, span sdktrace.ReadOnlySpan, expected map[attribute.Key]string) {
	t.Helper()
	actual := make(map[attribute.Key]string)
	for _, attr := range span.Attributes() {
		actual[attr.Key] = attr.Value.AsString()
	}
	for key, value := range expected {
		assert.Equal(t, value, actual[key], string(key))
	}
}

func TestUnchangedHistoricalPausedServiceIsNotAnAppliedTarget(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			current := nativeMinimalServiceSpec("api")
			observed := swarm.StackService{ID: "service-id", Name: "api", Mode: "replicated", Replicas: current.Mode.Replicated.Replicas, ServiceSpec: current, DesiredTasks: 1, UpdateStatus: &swarm.ServiceUpdateStatus{State: "paused"}}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{observed}, nil)
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{ID: "service-id", Spec: current, UpdateStatus: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}}, nil)
			d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, time.Millisecond, time.Second)}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			var err error
			if single {
				_, err = d.DeployService(t.Context(), "demo", "source.yaml", "api", minimalCompose("api"), compose.Compose{})
			} else {
				_, err = d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"))
			}
			require.NoError(t, err)
			// gomock permits no update: desired ServiceSpec was already accepted.
		})
	}
}

func TestRetryPausedStackActuallyAppliesNewTaskGeneration(t *testing.T) {
	manager := swarm.NewMockServiceManager(gomock.NewController(t))
	now := time.Now().UTC()
	current := nativeMinimalServiceSpec("api")
	current.TaskTemplate.ForceUpdate = 7
	before := swarm.StackService{ID: "service-id", Name: "api", Mode: "replicated", DesiredTasks: 1, Replicas: current.Mode.Replicated.Replicas, ServiceSpec: current, UpdatedAt: now.Add(-time.Hour), UpdateStatus: &swarm.ServiceUpdateStatus{State: "paused", StartedAt: now.Add(-time.Hour)}}
	after := before
	after.UpdatedAt = now
	after.ServiceSpec = nativeMinimalServiceSpec("api")
	after.ServiceSpec.TaskTemplate.ForceUpdate = 8
	after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: now}
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{before}, nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{ID: "service-id", Spec: current, UpdateStatus: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}}, nil)
	manager.EXPECT().Update(gomock.Any(), "service-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec, _ dockerswarm.ServiceUpdateOptions) error {
			assert.Equal(t, after.ServiceSpec.TaskTemplate.ForceUpdate, spec.TaskTemplate.ForceUpdate)
			return nil
		})
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{after}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "service-id").Return([]swarm.ServiceTask{{ID: "new-task", DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, CreatedAt: now}}, nil)
	d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, time.Millisecond, time.Second)}
	wireNativeTestDeployerWithServiceManager(t, d, manager, false)
	_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"), StackOptions{RetryFailedUpdates: true})
	require.NoError(t, err)
}

func TestAcceptedUnchangedRolloutIsVerified(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			now := time.Now().UTC()
			current := nativeMinimalServiceSpec("api")
			before := swarm.StackService{
				ID: "service-id", Name: "api", Mode: "replicated", DesiredTasks: 1, ServiceSpec: current,
				UpdatedAt: now.Add(-time.Minute), UpdateStatus: &swarm.ServiceUpdateStatus{State: "updating", StartedAt: now.Add(-time.Minute)},
			}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{before}, nil).Times(2)
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{ID: "service-id", Spec: current, UpdateStatus: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStateUpdating}}, nil)
			// Even a satisfied running count cannot complete an in-progress update.
			task := swarm.ServiceTask{ID: "task", DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, CreatedAt: now}
			manager.EXPECT().ListTasks(gomock.Any(), "service-id").Return([]swarm.ServiceTask{task}, nil).Times(2)
			after := before
			after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: now.Add(-time.Minute), CompletedAt: now}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{after}, nil)
			d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, time.Millisecond, time.Second)}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			var options []StackOptions
			if retry {
				options = append(options, StackOptions{RetryFailedUpdates: true})
			}
			_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", minimalCompose("api"), options...)
			require.NoError(t, err)
		})
	}
}

func TestFailuresDistinguishPreApplyChecksFromPartialServiceApply(t *testing.T) {
	for _, preApply := range []bool{false, true} {
		t.Run(fmt.Sprint(preApply), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			d := &Deployer{secretResolver: newTestSecretResolver()}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			desired := minimalCompose("api")
			if preApply {
				desired.Services[0].Extra = map[string]interface{}{"unsupported_test_option": true}
			} else {
				manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
				manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("", assert.AnError)
			}
			result, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", desired)
			require.Error(t, err)
			if preApply {
				require.NotNil(t, result.UnresolvedServices, "known pre-apply failure cannot invent a failed service generation")
				assert.Empty(t, result.UnresolvedServices)
			} else {
				assert.Nil(t, result.UnresolvedServices, "partial service apply needs conservative verification")
			}
		})
	}
}
