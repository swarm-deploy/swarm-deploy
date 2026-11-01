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
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPrepareRecoversStepPanicWithStage(t *testing.T) {
	cfg := &config.Config{Spec: config.Spec{SecretRotation: config.SecretRotationSpec{Enabled: true}}}
	// A missing rotator deliberately causes a panic inside the rotation step.
	preparer := New(cfg, nil, fs.NewLocalFileSystem(), nil)
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
	repository := gitx.NewRepository(config.GitSpec{}, dir)
	preparer := New(cfg, repository, fs.NewLocalFileSystem(), nil)
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
	assert.Equal(t, labelsdict.ServiceManagedLabelValue, prepared.Compose.Services[0].Deploy.Labels.Map[labelsdict.ServiceManagedLabelKey])
}

func TestPrepareKeepsExistingManagedLabelWithoutRendering(t *testing.T) {
	cfg := &config.Config{}
	preparer := New(cfg, nil, fs.NewLocalFileSystem(), nil)
	prepared, err := preparer.Prepare(t.Context(), Request{
		Stack: config.StackSpec{Name: "app"},
		Desired: &compose.File{
			Path: "/repo/app.yaml",
			Compose: compose.Compose{Services: compose.Services{{
				Name: "api",
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
	cfg := &config.Config{Spec: config.Spec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{
		Image: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}},
	}}}}
	preparer := New(cfg, nil, fs.NewLocalFileSystem(), nil)
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

func TestImagePolicyUsesInterpolatedSource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services: {api: {image: 'nginx:${SD_TEST_TAG}'}}"), 0o600))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data"), Sync: config.SyncSpec{
		Policy: config.SyncPolicySpec{Image: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}},
	}}}
	repository := gitx.NewRepository(config.GitSpec{}, dir)
	for _, tag := range []string{"1.27", "latest", "private value"} {
		t.Run(tag, func(t *testing.T) {
			t.Setenv("SD_TEST_TAG", tag)
			preparer := New(cfg, repository, fs.NewLocalFileSystem(), nil)
			source, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "compose.yaml"})
			require.NoError(t, err)
			assert.Equal(t, "nginx:"+tag, source.Compose.Services[0].Image)
			err = ValidateImagePolicy(cfg.Spec.Sync.Policy.Image, source)
			if tag != "1.27" {
				var validation *compose.ValidateComposeError
				require.ErrorAs(t, err, &validation)
				code := ImageViolationLatest
				if tag == "private value" {
					code = ImageViolationInvalid
				}
				assert.Equal(t, code, validation.Issues[0].Code)
				assert.NotContains(t, err.Error(), "nginx:"+tag)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
