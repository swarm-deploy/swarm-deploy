package preparation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPrepareRecoversStepPanicWithStage(t *testing.T) {
	cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{ResourceRotation: config.ResourceRotationSpec{Enabled: true}}}}
	// A missing rotator deliberately causes a panic inside the rotation step.
	preparer := New(cfg, nil, nil)
	_, err := preparer.Prepare(t.Context(), Request{
		Stack:   config.StackSpec{Name: "app"},
		Desired: &compose.File{Path: "/app/compose.yaml"},
	})
	var stepErr *pipe.StepError
	require.ErrorAs(t, err, &stepErr)
	assert.Equal(t, "rotate secrets/configs", stepErr.StepName)
	assert.Contains(t, err.Error(), "step panicked")
}

func TestPrepareRecordsOriginalStepSpans(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })

	dir := t.TempDir()
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
	preparer := New(cfg, nil, nil)
	preparer.attachPipeline(provider)

	prepared, err := preparer.Prepare(t.Context(), Request{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{
			Path: filepath.Join(dir, "app.yaml"),
			Compose: compose.Compose{Services: compose.Services{{
				Name: "api", Image: "nginx:1.27",
			}}},
		},
	})
	require.NoError(t, err)

	var names []string
	for _, span := range spans.Ended() {
		names = append(names, span.Name())
	}
	assert.Contains(t, names, "prepare stack")
	assert.Contains(t, names, "add managed label")
	assert.Contains(t, names, "validate desired state")
	assert.Contains(t, names, "recalculate service digests")
	assert.Equal(t, labelsdict.ServiceManagedLabelValue, prepared.Compose.Services[0].Deploy.Labels.Map[labelsdict.ServiceManagedLabelKey])
	assert.NotEmpty(t, prepared.ServiceDigests["api"])
}

func TestPrepareKeepsExistingManagedLabelWithoutRendering(t *testing.T) {
	cfg := &config.Config{}
	preparer := New(cfg, nil, nil)
	prepared, err := preparer.Prepare(t.Context(), Request{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{
			Path: "/repo/app.yaml",
			Compose: compose.Compose{Services: compose.Services{{
				Name: "api", Image: "nginx:1.27",
				Deploy: compose.ServiceDeploy{Labels: *compose.NewLabels(map[string]string{
					labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
				})},
			}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "/repo/app.yaml", prepared.Path)
}

func TestPreparePreservesValidationErrorType(t *testing.T) {
	cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{
		Image: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}},
	}}}}}
	preparer := New(cfg, nil, nil)
	_, err := preparer.Prepare(t.Context(), Request{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{Path: "/repo/app.yaml", Compose: compose.Compose{
			Services: compose.Services{{Name: "api", Image: "nginx:latest"}},
		}},
	})
	var validationErr *compose.ValidateComposeError
	require.True(t, errors.As(err, &validationErr))
	var stepErr *pipe.StepError
	require.ErrorAs(t, err, &stepErr)
	assert.Equal(t, "check image policy", stepErr.StepName)
}

func TestPrepareValidatesEffectiveCompose(t *testing.T) {
	preparer := New(&config.Config{}, nil, nil)
	_, err := preparer.Prepare(t.Context(), Request{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{Path: "/repo/app.yaml", Compose: compose.Compose{
			Services: compose.Services{{Name: "api", Image: "private/INVALID"}},
		}},
	})
	var stepErr *pipe.StepError
	require.ErrorAs(t, err, &stepErr)
	assert.Equal(t, "validate desired state", stepErr.StepName)
	var validation *compose.ValidateComposeError
	require.ErrorAs(t, err, &validation)
	assert.Equal(t, `services.api: image: invalid reference "private/INVALID"`, validation.FaultSummary())
}
