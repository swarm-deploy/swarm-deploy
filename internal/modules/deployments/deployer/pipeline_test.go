package deployer

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"testing"
	"time"

	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"

	pipe "github.com/artarts36/gopipe"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
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
		tracing.ResourceStackName: "demo", tracing.ResourceComposePath: "source.yaml",
	})
}

func TestDeployPipelineFailureMasksCredentials(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "password assignment", raw: "password=private-value", want: "password=****"},
		{name: "password field", raw: "password: private-value", want: "password: ****"},
		{name: "token assignment", raw: "token=private-value", want: "token=****"},
		{name: "HTTP credentials", raw: "https://private-user:private-value@registry.example/path",
			want: "https://****:****@registry.example/path"},
		{name: "PostgreSQL DSN", raw: "postgres://private-user:private-value@db.example/db", want: "postgres://****"},
		{name: "bare DSN", raw: "private-user:private-value@db.example", want: "****:****@db.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := setupPipelineTracing(t)
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			cause := errors.New("registry failed: " + tt.raw + " (connection refused)")
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
			manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("", cause)
			deployer := &Deployer{
				secretResolver: newTestSecretResolver(), initJobRunner: &fakeInitJobExecutor{},
				serviceWaiter: newSuccessfulServiceWaiter(t),
			}
			wireNativeTestDeployerWithServiceManager(t, deployer, manager, false)

			err := runMinimalStack(t, deployer)
			require.ErrorIs(t, err, cause)
			want := "registry failed: " + tt.want + " (connection refused)"
			assert.Contains(t, err.Error(), want)
			assert.NotContains(t, err.Error(), "private-value")
			assert.NotContains(t, err.Error(), "private-user")

			output := logs.String()
			assert.Contains(t, output, `"msg":"[gopipe] step failed"`)
			assert.Contains(t, output, want)
			assert.NotContains(t, output, "private-value")
			assert.NotContains(t, output, "private-user")

			spans := recorder.Ended()
			for _, name := range []string{stepApplyStack, deployStackPipelineName, deployStackOperationName} {
				span := requireSpan(t, spans, name)
				assert.Equal(t, codes.Error, span.Status().Code, name)
				for _, diagnostic := range []string{span.Status().Description, fmt.Sprint(span.Events())} {
					assert.Contains(t, diagnostic, want, name)
					assert.NotContains(t, diagnostic, "private-value", name)
					assert.NotContains(t, diagnostic, "private-user", name)
				}
			}
		})
	}
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

	result, err := deployer.DeployStack(t.Context(), StackRequest{
		StackName:         "demo",
		SourceComposePath: "source.yaml",
		Desired:           desired,
	})
	require.Error(t, err)
	require.Len(t, result.InitJobs, 1)
	assert.ErrorIs(t, err, jobErr)
	var stepErr *pipe.StepError
	require.True(t, errors.As(err, &stepErr))
	assert.Equal(t, stepRunInitJobs, stepErr.StepName)
}

func TestPipelineErrorPreservesCauseAndClassification(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(fmt.Sprintf("temporary=%t", temporary), func(t *testing.T) {
			cause := &net.DNSError{Err: "lookup failed", IsTemporary: temporary}
			wrapped := fmt.Errorf("registry failed: %w", cause)
			err := safePipelineError(wrapped)
			assert.Same(t, cause, errors.Unwrap(errors.Unwrap(err)))
			assert.ErrorIs(t, err, cause)
			var lookup *net.DNSError
			require.ErrorAs(t, err, &lookup)
			assert.Same(t, cause, lookup)
			var classified faults.Error
			require.ErrorAs(t, err, &classified)
			assert.Equal(t, temporary, classified.Temporary())
		})
	}
}

func TestDeployPipelinesShareLiteralDesiredState(t *testing.T) {
	t.Setenv("SD_TEST_TAG", "3")
	t.Setenv("SD_TEST_TEXT", "$$HOME ${SD_TEST_TAG}")
	loader := compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
		return []byte(`services:
  api:
    image: 'alpine:3'
    command: ['sh', '-c', 'echo $$HOME $$(date) $$$$ ${SD_TEST_TEXT}']
    entrypoint: ['/bin/${SD_TEST_SHELL:-sh}']
    environment: {ESCAPED: '$$HOME', INSERTED: '${SD_TEST_TEXT}'}
    healthcheck: {test: [CMD-SHELL, 'test -n "$$HOME"']}
`), nil
	})
	file, err := loader.Load(t.Context(), "source.yaml")
	require.NoError(t, err)
	// Remediation must preserve literal values and must not expand them
	// when the controller environment changes.
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
	_, err = deployer.DeployStack(t.Context(), StackRequest{
		StackName:         "demo",
		SourceComposePath: "source.yaml",
		Desired:           file.Compose,
	})
	require.NoError(t, err)
	_, err = deployer.DeployService(t.Context(), ServiceRequest{
		StackName:         "demo",
		SourceComposePath: "source.yaml",
		ServiceName:       "api",
		Desired:           restored,
	})
	require.NoError(t, err)
	require.Len(t, applied, 2)
	assert.Equal(t, applied[0], applied[1])
	container := applied[0].TaskTemplate.ContainerSpec
	assert.Equal(t, "alpine:3", container.Image)
	assert.Equal(t, []string{"sh", "-c", "echo $$HOME $$(date) $$$$ ${SD_TEST_TEXT}"}, container.Args)
	assert.ElementsMatch(t, []string{"ESCAPED=$$HOME", "INSERTED=${SD_TEST_TEXT}"}, container.Env)
}

func runMinimalStack(t *testing.T, deployer *Deployer) error {
	t.Helper()
	_, err := deployer.DeployStack(t.Context(), StackRequest{
		StackName:         "demo",
		SourceComposePath: "source.yaml",
		Desired:           minimalCompose("api"),
	})
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

func TestUnchangedPausedServiceIsVerifiedForRemediation(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(fmt.Sprint(single), func(t *testing.T) {
			manager := swarm.NewMockServiceManager(gomock.NewController(t))
			current := nativeMinimalServiceSpec("api")
			observed := swarm.StackService{ID: "service-id", Name: "api", Mode: "replicated", Replicas: current.Mode.Replicated.Replicas, ServiceSpec: current, DesiredTasks: 1, UpdateStatus: &swarm.ServiceUpdateStatus{State: "paused"}}
			reads := 1
			if single {
				reads++
				manager.EXPECT().ListTasks(gomock.Any(), observed.ID).Return(nil, nil)
			}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{observed}, nil).Times(reads)
			manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{ID: "service-id", Spec: current, UpdateStatus: &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused}}, nil)
			d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, nil, time.Millisecond, time.Second)}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			var err error
			if single {
				_, err = d.DeployService(t.Context(), ServiceRequest{
					StackName:         "demo",
					SourceComposePath: "source.yaml",
					ServiceName:       "api",
					Desired:           minimalCompose("api"),
				})
			} else {
				_, err = d.DeployStack(t.Context(), StackRequest{
					StackName:         "demo",
					SourceComposePath: "source.yaml",
					Desired:           minimalCompose("api"),
				})
			}
			if single {
				require.ErrorIs(t, err, waiterpkg.ErrConvergenceFailed)
			} else {
				require.NoError(t, err)
			}
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
	manager.EXPECT().ListTasks(gomock.Any(), "service-id").Return([]swarm.ServiceTask{{Spec: swarm.NewTaskSpec(after.ServiceSpec.TaskTemplate), ID: "new-task", DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, CreatedAt: now}}, nil)
	d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, nil, time.Millisecond, time.Second)}
	wireNativeTestDeployerWithServiceManager(t, d, manager, false)
	_, err := d.DeployStack(t.Context(), StackRequest{
		StackName:         "demo",
		SourceComposePath: "source.yaml",
		Desired:           minimalCompose("api"),
		Options:           StackOptions{RetryFailedUpdates: true},
	})
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
			task := swarm.ServiceTask{Spec: swarm.NewTaskSpec(current.TaskTemplate), ID: "task", DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, CreatedAt: now}
			manager.EXPECT().ListTasks(gomock.Any(), "service-id").Return([]swarm.ServiceTask{task}, nil).Times(2)
			after := before
			after.UpdateStatus = &swarm.ServiceUpdateStatus{State: "completed", StartedAt: now.Add(-time.Minute), CompletedAt: now}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).Return([]swarm.StackService{after}, nil)
			d := &Deployer{secretResolver: newTestSecretResolver(), serviceWaiter: waiterpkg.NewServiceWaiter(manager, nil, time.Millisecond, time.Second)}
			wireNativeTestDeployerWithServiceManager(t, d, manager, false)
			options := StackOptions{}
			if retry {
				options.RetryFailedUpdates = true
			}
			_, err := d.DeployStack(t.Context(), StackRequest{
				StackName:         "demo",
				SourceComposePath: "source.yaml",
				Desired:           minimalCompose("api"),
				Options:           options,
			})
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
				desired.Services[0].Image = ""
			} else {
				manager.EXPECT().Inspect(gomock.Any(), "demo_api").Return(dockerswarm.Service{}, swarm.ErrServiceNotFound)
				manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return("", assert.AnError)
			}
			result, err := d.DeployStack(t.Context(), StackRequest{
				StackName:         "demo",
				SourceComposePath: "source.yaml",
				Desired:           desired,
			})
			require.Error(t, err)
			if preApply {
				assert.True(t, result.Verification.Known, "known pre-apply failure cannot invent a failed service generation")
				assert.Empty(t, result.Verification.UnresolvedServices)
			} else {
				assert.False(t, result.Verification.Known, "partial service apply needs conservative verification")
			}
		})
	}
}

func TestPlacementOnlyPartialApplyRecoversUnresolvedServiceWithoutNewUpdate(t *testing.T) {
	controller := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(controller)
	nodes := swarm.NewMockNodeManager(controller)
	now := time.Now().UTC()
	replicas := uint64(1)
	beforeSpec := nativeMinimalServiceSpec("a")
	before := swarm.StackService{
		ID: "a-id", Name: "a", Mode: "replicated", Replicas: &replicas, DesiredTasks: replicas,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), ServiceSpec: beforeSpec,
	}
	desired := minimalCompose("a", "b")
	desired.Services[0].Deploy.Placement = &compose.ServiceDeployPlacement{
		Constraints: []string{"node.labels.zone == east"},
	}

	var acceptedSpec dockerswarm.ServiceSpec
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).
		Return([]swarm.StackService{before}, nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_a").Return(dockerswarm.Service{
		ID: "a-id", Spec: beforeSpec,
		Meta: dockerswarm.Meta{CreatedAt: before.CreatedAt, UpdatedAt: before.UpdatedAt},
	}, nil)
	manager.EXPECT().Update(gomock.Any(), "a-id", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, _ dockerswarm.Version, spec dockerswarm.ServiceSpec,
			_ dockerswarm.ServiceUpdateOptions,
		) error {
			acceptedSpec = spec
			return nil
		})
	manager.EXPECT().Inspect(gomock.Any(), "demo_b").Return(dockerswarm.Service{}, assert.AnError)

	d := &Deployer{
		secretResolver: newTestSecretResolver(),
		serviceWaiter:  waiterpkg.NewServiceWaiter(manager, nodes, time.Millisecond, time.Second),
	}
	wireNativeTestDeployerWithServiceManager(t, d, manager, false)
	first, err := d.DeployStack(t.Context(), StackRequest{
		StackName: "demo", SourceComposePath: "source.yaml", Desired: desired,
	})
	require.ErrorIs(t, err, assert.AnError)
	assert.False(t, first.Verification.Known)
	require.NotNil(t, acceptedSpec.TaskTemplate.Placement)

	previous := beforeSpec
	accepted := swarm.StackService{
		ID: "a-id", Name: "a", Mode: "replicated", Replicas: &replicas, DesiredTasks: replicas,
		CreatedAt: before.CreatedAt, UpdatedAt: now, ServiceSpec: acceptedSpec, PreviousSpec: &previous,
	}
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).
		Return([]swarm.StackService{accepted}, nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_a").Return(dockerswarm.Service{
		ID: "a-id", Spec: acceptedSpec,
		Meta:         dockerswarm.Meta{CreatedAt: before.CreatedAt, UpdatedAt: now},
		PreviousSpec: &previous,
	}, nil)
	manager.EXPECT().Inspect(gomock.Any(), "demo_b").Return(dockerswarm.Service{
		ID: "b-id", Spec: nativeMinimalServiceSpec("b"),
	}, nil)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).
		Return([]swarm.StackService{accepted}, nil)
	manager.EXPECT().ListTasks(gomock.Any(), "a-id").Return([]swarm.ServiceTask{{
		ID: "preserved-task", Node: "node-east", DesiredState: swarm.TaskStateRunning,
		CurrentState: swarm.TaskStateRunning, CreatedAt: before.CreatedAt, Spec: swarm.NewTaskSpec(beforeSpec.TaskTemplate),
	}}, nil)
	nodes.EXPECT().List(gomock.Any()).Return([]swarm.Node{{
		ID: "node-east", Role: "worker", Labels: map[string]string{"zone": "east"},
	}}, nil)

	second, err := d.DeployStack(t.Context(), StackRequest{
		StackName: "demo", SourceComposePath: "source.yaml", Desired: desired,
		Options: StackOptions{UnresolvedServices: []string{"a"}},
	})

	require.NoError(t, err)
	assert.True(t, second.Verification.Known)
	assert.Empty(t, second.Verification.UnresolvedServices)
}
