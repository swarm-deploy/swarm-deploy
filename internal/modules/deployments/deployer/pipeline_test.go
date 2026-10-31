package deployer

import (
	"context"
	"errors"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestDeployPipelinesExposeOrderedStages(t *testing.T) {
	for _, testCase := range []struct {
		name string
		run  func(*Deployer) error
		want []string
	}{
		{
			name: "stack",
			run: func(d *Deployer) error {
				_, err := d.DeployStack(t.Context(), "demo", "source.yaml", "rendered.yaml", compose.Compose{
					Services: compose.Services{{Name: "api"}},
				})
				return err
			},
			want: []string{stepReconcileResources, stepRunInitJobs, stepCaptureBaseline, stepApplyStack, stepWaitConvergence},
		},
		{
			name: "service",
			run: func(d *Deployer) error {
				desired := compose.Compose{Services: compose.Services{{Name: "api"}}}
				_, err := d.DeployService(t.Context(), "demo", "source.yaml", "api", desired, desired)
				return err
			},
			want: []string{stepPrepareService, stepReconcileResources, stepCaptureBaseline, stepApplyService, stepWaitConvergence},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sdktrace.AlwaysSample()))
			previous := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				require.NoError(t, provider.Shutdown(context.Background()))
			})

			d := &Deployer{
				secretResolver: newTestSecretResolver(), stackDeployArgs: []string{"stack", "deploy"},
				runner: &fakeRunner{}, initJobRunner: &fakeInitJobExecutor{}, serviceWaiter: newSuccessfulServiceWaiter(t),
			}
			require.NoError(t, testCase.run(d))

			got := make([]string, 0, len(testCase.want))
			for _, span := range recorder.Ended() {
				for _, expected := range testCase.want {
					if span.Name() == expected {
						got = append(got, span.Name())
					}
				}
			}
			assert.Equal(t, testCase.want, got)
		})
	}
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
