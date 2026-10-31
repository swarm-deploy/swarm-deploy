package deployer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
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
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
)

func TestDeployPipelinesPreserveTraceHierarchyAndRootAttributes(t *testing.T) {
	recorder, testTracer := setupPipelineTracing(t)
	for _, testCase := range []struct {
		name               string
		operationName      string
		pipelineName       string
		applyStage         string
		run                func(*Deployer) error
		wantStages         []string
		wantRootAttributes map[attribute.Key]string
	}{
		{
			name:          "stack",
			operationName: deployStackOperationName,
			pipelineName:  deployStackPipelineName,
			applyStage:    stepApplyStack,
			run: func(d *Deployer) error {
				_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
					Services: compose.Services{{Name: "api"}},
				})
				return err
			},
			wantStages: []string{
				stepReconcileResources, stepRunInitJobs, stepCaptureBaseline, stepApplyStack, stepWaitConvergence,
			},
			wantRootAttributes: map[attribute.Key]string{
				tracing.ResourceStackName:   "demo",
				tracing.ResourceComposePath: "rendered.yaml",
			},
		},
		{
			name:          "service",
			operationName: deployServiceOperationName,
			pipelineName:  deployServicePipelineName,
			applyStage:    stepApplyService,
			run: func(d *Deployer) error {
				desired := compose.Compose{Services: compose.Services{{Name: "api"}}}
				_, err := d.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
				return err
			},
			wantStages: []string{
				stepPrepareService, stepReconcileResources, stepCaptureBaseline, stepApplyService, stepWaitConvergence,
			},
			wantRootAttributes: map[attribute.Key]string{
				tracing.ResourceStackName:   "demo",
				tracing.ResourceServiceName: "api",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spanOffset := len(recorder.Ended())
			ctrl := gomock.NewController(t)
			manager := swarm.NewMockServiceManager(ctrl)
			baselineCall := manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).
				DoAndReturn(func(ctx context.Context, _ swarm.ListServicesFilter) ([]swarm.StackService, error) {
					recordTestSpan(ctx, testTracer, "docker.snapshot")
					return nil, nil
				})
			replicas := uint64(0)
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "demo"}).After(baselineCall).
				DoAndReturn(func(ctx context.Context, _ swarm.ListServicesFilter) ([]swarm.StackService, error) {
					recordTestSpan(ctx, testTracer, "docker.observe.services")
					return []swarm.StackService{{
						ID: "api-id", Name: "api", Mode: "replicated", Replicas: &replicas, CreatedAt: time.Now(),
					}}, nil
				})
			manager.EXPECT().ListTasks(gomock.Any(), "api-id").
				DoAndReturn(func(ctx context.Context, _ string) ([]swarm.ServiceTask, error) {
					recordTestSpan(ctx, testTracer, "docker.observe.tasks")
					return nil, nil
				})

			runner := NewMockRunner(ctrl)
			runner.EXPECT().Run(
				gomock.Any(), "stack", "deploy", "-c", gomock.Any(), "demo",
			).DoAndReturn(func(ctx context.Context, _ ...string) (string, error) {
				recordTestSpan(ctx, testTracer, "dockerCLI.Run")
				return "", nil
			})

			d := &Deployer{
				secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
				runner: runner, initJobRunner: NewMockInitJobExecutor(ctrl),
				serviceWaiter: NewServiceWaiter(manager, time.Millisecond, time.Second),
			}
			require.NoError(t, testCase.run(d))

			spans := recorder.Ended()[spanOffset:]
			root := requireSpan(t, spans, testCase.operationName)
			assert.Equal(t, codes.Ok, root.Status().Code)
			assertSpanAttributes(t, root, testCase.wantRootAttributes)
			assert.Equal(t, 1, spanCount(spans, testCase.operationName), "operation span must be unique")

			pipeline := requireSpan(t, spans, testCase.pipelineName)
			assert.Equal(t, codes.Unset, pipeline.Status().Code)
			assert.Equal(t, root.SpanContext().SpanID(), pipeline.Parent().SpanID(), "pipeline parent")
			assert.Equal(t, 1, spanCount(spans, testCase.pipelineName), "pipeline span must be unique")

			stageSpans := make(map[string]sdktrace.ReadOnlySpan, len(testCase.wantStages))
			var previousStage sdktrace.ReadOnlySpan
			for _, name := range testCase.wantStages {
				stage := requireSpan(t, spans, name)
				stageSpans[name] = stage
				assert.Equal(t, pipeline.SpanContext().SpanID(), stage.Parent().SpanID(), "%s parent", name)
				assertSpanAttributes(t, stage, testCase.wantRootAttributes)
				if previousStage != nil {
					assert.False(t, stage.StartTime().Before(previousStage.StartTime()), "%s stage order", name)
				}
				previousStage = stage
			}

			baseline := stageSpans[stepCaptureBaseline]
			apply := stageSpans[testCase.applyStage]
			wait := stageSpans[stepWaitConvergence]
			assert.Equal(t, baseline.SpanContext().SpanID(), requireSpan(t, spans, "docker.snapshot").Parent().SpanID())
			assert.Equal(t, apply.SpanContext().SpanID(), requireSpan(t, spans, "dockerCLI.Run").Parent().SpanID())
			waiter := requireSpan(t, spans, "serviceWaiter.Wait")
			assert.Equal(t, wait.SpanContext().SpanID(), waiter.Parent().SpanID())
			assert.NotEqual(t, baseline.SpanContext().SpanID(), requireSpan(t, spans, "dockerCLI.Run").Parent().SpanID())
			assert.NotEqual(t, baseline.SpanContext().SpanID(), waiter.Parent().SpanID())
			assert.Equal(t, waiter.SpanContext().SpanID(), requireSpan(t, spans, "docker.observe.services").Parent().SpanID())
			assert.Equal(t, waiter.SpanContext().SpanID(), requireSpan(t, spans, "docker.observe.tasks").Parent().SpanID())
		})
	}
}

func TestDeployPipelineFailureSetsSafeRootSpanError(t *testing.T) {
	recorder, _ := setupPipelineTracing(t)
	ctrl := gomock.NewController(t)
	waiter := NewMockServiceWaitExecutor(ctrl)
	waiter.EXPECT().Snapshot(gomock.Any(), "demo").Return(nil, nil)
	runner := NewMockRunner(ctrl)
	runner.EXPECT().Run(gomock.Any(), "stack", "deploy", "-c", "rendered.yaml", "demo").
		Return("", errors.New("registry failed: token=private-value"))
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
		runner: runner, initJobRunner: NewMockInitJobExecutor(ctrl), serviceWaiter: waiter,
	}

	_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
		Services: compose.Services{{Name: "api"}},
	})
	require.Error(t, err)

	spans := recorder.Ended()
	root := requireSpan(t, spans, deployStackOperationName)
	assert.Equal(t, codes.Error, root.Status().Code)
	assert.NotContains(t, fmt.Sprint(root.Status(), root.Events()), "private-value")
	assert.Contains(t, fmt.Sprint(root.Status(), root.Events()), "token=****")
	assertSpanAttributes(t, root, map[attribute.Key]string{
		tracing.ResourceStackName:   "demo",
		tracing.ResourceComposePath: "rendered.yaml",
	})

	pipeline := requireSpan(t, spans, deployStackPipelineName)
	assert.Equal(t, codes.Error, pipeline.Status().Code)
	assert.Equal(t, root.SpanContext().SpanID(), pipeline.Parent().SpanID())
	assert.NotContains(t, fmt.Sprint(pipeline.Status(), pipeline.Events()), "private-value")
	assert.Contains(t, fmt.Sprint(pipeline.Status(), pipeline.Events()), "token=****")
	assert.Equal(t, 1, spanCount(spans, deployStackOperationName))
	assert.Equal(t, 1, spanCount(spans, deployStackPipelineName))
}

func TestDeployStackStepErrorPreservesJobFailureType(t *testing.T) {
	jobErr := &JobFailedError{ID: "task-id", Name: "migrate", Reason: "exit code 1"}
	d := &Deployer{
		secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
		runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{errAt: 1, err: jobErr},
		serviceWaiter: newSuccessfulServiceWaiter(t),
	}

	result, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
		Services: compose.Services{{Name: "api", InitJobs: []compose.InitJob{{Name: "migrate"}}}},
	})
	require.Error(t, err)
	require.Len(t, result.InitJobs, 1)
	assert.ErrorIs(t, err, jobErr)
	var typed *JobFailedError
	assert.True(t, errors.As(err, &typed))
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

func setupPipelineTracing(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
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
	return recorder, provider.Tracer("deployer-pipeline-test")
}

func recordTestSpan(ctx context.Context, tracer trace.Tracer, name string) {
	_, span := tracer.Start(ctx, name)
	span.End()
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

func spanCount(spans []sdktrace.ReadOnlySpan, name string) int {
	count := 0
	for _, span := range spans {
		if span.Name() == name {
			count++
		}
	}
	return count
}

func assertSpanAttributes(
	t *testing.T,
	span sdktrace.ReadOnlySpan,
	want map[attribute.Key]string,
) {
	t.Helper()
	got := make(map[attribute.Key]string, len(span.Attributes()))
	for _, attr := range span.Attributes() {
		got[attr.Key] = attr.Value.AsString()
	}
	for key, value := range want {
		assert.Equal(t, value, got[key], "span attribute %s", key)
	}
}
