package preparation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
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
	assert.Equal(t, labelsdict.ServiceManagedLabelValue, prepared.Compose.Services[0].Deploy.Labels.Map[labelsdict.ServiceManagedLabelKey])
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

func TestImagePolicyKeepsLiteralSource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services: {api: {image: 'nginx:${SD_TEST_TAG}'}}"), 0o600))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data"), GitOps: config.GitOpsSpec{Sync: config.SyncSpec{
		Policy: config.SyncPolicySpec{Image: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}},
	}}}}
	repository := gitx.NewMockRepository(gomock.NewController(t))
	repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
	repository.EXPECT().ReadFile(gomock.Any(), "test-commit", "compose.yaml").
		DoAndReturn(func(_ context.Context, _, path string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, path))
		}).AnyTimes()
	for _, tag := range []string{"1.27", "latest", "private value"} {
		t.Run(tag, func(t *testing.T) {
			t.Setenv("SD_TEST_TAG", tag)
			preparer := New(cfg, repository, nil)
			source, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "compose.yaml"}, "test-commit")
			require.NoError(t, err)
			assert.Equal(t, "nginx:${SD_TEST_TAG}", source.Compose.Services[0].Image)
			err = ValidateImagePolicy(cfg.Spec.GitOps.Sync.Policy.Image, source)
			var validation *compose.ValidateComposeError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, ImageViolationInvalid, validation.Issues[0].Code)
			assert.NotContains(t, err.Error(), "nginx:"+tag)
		})
	}
}
