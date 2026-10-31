package deployer

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
