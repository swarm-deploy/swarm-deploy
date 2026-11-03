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
	"go.uber.org/mock/gomock"
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

func TestLoadReadsRepositoryFilesAtRevision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	outside := filepath.Join(t.TempDir(), "mounted.secret")
	require.NoError(t, os.WriteFile(outside, []byte("mounted"), 0o600))
	linkedOutside := filepath.Join(t.TempDir(), "linked.secret")
	require.NoError(t, os.WriteFile(linkedOutside, []byte("linked"), 0o600))
	files := map[string]string{
		"app/compose.yaml": "services:\n  api:\n    image: nginx:1.27\n    env_file: [../shared/app.env]\n" +
			"configs:\n  settings:\n    file: ./settings.conf\nsecrets:\n  mounted:\n    file: " + outside + "\n" +
			"  linked:\n    file: ./linked.secret\n",
		"shared/app.env":    "MODE=revision\n",
		"app/settings.conf": "revision settings",
	}
	repository := gitx.NewMockRepository(gomock.NewController(t))
	repository.EXPECT().WorkingDir().Return(root)
	for path, content := range files {
		repository.EXPECT().ReadFileAt(gomock.Any(), "abc123", path).Return([]byte(content), nil)
	}
	// A symlink in the revision that leaves the repository follows the external file policy.
	repository.EXPECT().ReadFileAt(gomock.Any(), "abc123", "app/linked.secret").
		Return(nil, &gitx.ExternalPathError{Path: linkedOutside})
	// The working tree is never read: none of the repository files exist on disk.
	preparer := New(&config.Config{}, repository, fs.NewLocalFileSystem(), nil)

	source, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "app/compose.yaml"}, "abc123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "app", "compose.yaml"), source.Path)
	assert.Equal(t, "revision", source.Compose.Services[0].EnvFiles[0].Variables["MODE"])
	assert.Equal(t, []byte("revision settings"), source.Compose.Configs["settings"].Data)
	assert.Equal(t, []byte("mounted"), source.Compose.Secrets["mounted"].Data,
		"absolute paths outside the repository are read from the filesystem")
	assert.Equal(t, []byte("linked"), source.Compose.Secrets["linked"].Data,
		"symlinks leaving the repository are read from the filesystem")
}

func TestRepositoryPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
		inside   bool
	}{
		{name: "nested file", path: "/repo/app/compose.yaml", expected: "app/compose.yaml", inside: true},
		{name: "normalized parent inside", path: "/repo/app/../shared/app.env", expected: "shared/app.env", inside: true},
		{name: "sibling directory", path: "/repository/app.env"},
		{name: "outside", path: "/run/secrets/token"},
		{name: "escape", path: "/repo/../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relative, inside := repositoryPath("/repo", tt.path)

			assert.Equal(t, tt.inside, inside)
			assert.Equal(t, tt.expected, relative)
		})
	}
}

func TestImagePolicyKeepsLiteralSource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"),
		[]byte("services: {api: {image: 'nginx:${SD_TEST_TAG}'}}"), 0o600))
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data"), Sync: config.SyncSpec{
		Policy: config.SyncPolicySpec{Image: config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{NoLatest: true}}},
	}}}
	repository := gitx.NewMockRepository(gomock.NewController(t))
	repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
	repository.EXPECT().ReadFileAt(gomock.Any(), "revision", "compose.yaml").DoAndReturn(
		func(context.Context, string, string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, "compose.yaml"))
		}).AnyTimes()
	for _, tag := range []string{"1.27", "latest", "private value"} {
		t.Run(tag, func(t *testing.T) {
			t.Setenv("SD_TEST_TAG", tag)
			preparer := New(cfg, repository, fs.NewLocalFileSystem(), nil)
			source, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "compose.yaml"}, "revision")
			require.NoError(t, err)
			assert.Equal(t, "nginx:${SD_TEST_TAG}", source.Compose.Services[0].Image)
			err = ValidateImagePolicy(cfg.Spec.Sync.Policy.Image, source)
			var validation *compose.ValidateComposeError
			require.ErrorAs(t, err, &validation)
			assert.Equal(t, ImageViolationInvalid, validation.Issues[0].Code)
			assert.NotContains(t, err.Error(), "nginx:"+tag)
		})
	}
}
