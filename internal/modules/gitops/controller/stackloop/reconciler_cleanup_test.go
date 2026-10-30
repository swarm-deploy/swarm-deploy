package stackloop

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

func TestReconcileRemovesRenderedComposeAfterEarlyPipelineFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")
	errAfterRender := errors.New("failed after render")
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spanRecorder),
	)
	t.Cleanup(func() { require.NoError(t, tracerProvider.Shutdown(context.Background())) })
	fileSystem := fs.NewTraceableFileSystem(tracerProvider, fs.NewLocalFileSystem())

	require.NoError(t, writeComposeFile(repoDir), "write compose")
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	reconciler := &Reconciler{
		cfg:              &config.Config{Spec: config.Spec{DataDir: filepath.Join(repoDir, ".data")}},
		git:              repository,
		event:            &dispatcher.NopDispatcher{},
		deployMetrics:    &metrics.NopDeploys{},
		stateStore:       stateStore,
		fileSystem:       fileSystem,
		composeLoader:    compose.NewFileLoader(),
		composeRotator:   NewRotator(fileSystem),
		envFilePopulator: compose.NewEnvFilePopulator(),
		pending:          map[string]pendingCompletion{},
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.pipeline = pipe.NewPipelineWithConfig[*pipelinePayload](pipe.Config{
		PipelineName: "test early failure",
		Logger:       faults.NewSafeLogger(slog.Default()),
	})
	reconciler.pipeline.Add(pipe.Step[*pipelinePayload]{Name: "add managed label", Run: reconciler.addManagedLabel})
	reconciler.pipeline.Add(pipe.Step[*pipelinePayload]{Name: "write rendered compose", Run: reconciler.writeRenderedCompose})
	reconciler.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "fail after render",
		Run: func(context.Context, *pipelinePayload) error {
			assert.FileExists(t, renderedPath, "rendered compose must exist until the pipeline stops using it")
			return errAfterRender
		},
	})

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack:  config.StackSpec{Name: "app", ComposeFile: "app.yaml"},
		Commit: "commit-early-failure",
	})

	require.Error(t, err, "expected reconciliation failure")
	assert.ErrorIs(t, err, errAfterRender, "unexpected reconciliation error")
	assert.NoFileExists(t, renderedPath, "rendered compose must be removed after early failure")
	spanNames := make([]string, 0, len(spanRecorder.Ended()))
	for _, span := range spanRecorder.Ended() {
		spanNames = append(spanNames, span.Name())
	}
	assert.ElementsMatch(t, []string{"fs.CreateDirectory", "fs.WriteFile", "fs.RemoveFile"}, spanNames)
}

func TestRenderedComposeCleanupFailureDoesNotFailReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	dataDir := filepath.Join(repoDir, ".data")
	composePath := filepath.Join(repoDir, "app.yaml")
	renderedPath := filepath.Join(dataDir, "rendered", "app.yaml")

	require.NoError(t, writeComposeFile(repoDir), "write compose")
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", composePath, renderedPath, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, deployComposePath string, _ compose.Compose) (deployer.Result, error) {
			require.FileExists(t, deployComposePath, "rendered compose must exist during deployment")
			require.NoError(t, os.Remove(deployComposePath), "remove rendered compose in deployer")
			require.NoError(t, os.Mkdir(deployComposePath, 0o700), "replace rendered compose with directory")
			require.NoError(t, os.WriteFile(filepath.Join(deployComposePath, "block"), []byte("block"), 0o600),
				"prevent directory removal")
			return deployer.Result{}, nil
		})
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	reconciler := &Reconciler{
		cfg:            &config.Config{Spec: config.Spec{DataDir: dataDir}},
		git:            repository,
		deployer:       stackDeployer,
		event:          &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		fileSystem:     fs.NewLocalFileSystem(),
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		driftAnalyzer:  nil,
		serviceManager: serviceManager,
		pending:        map[string]pendingCompletion{},
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack:  config.StackSpec{Name: "app", ComposeFile: "app.yaml"},
		Commit: "commit-cleanup-failure",
	})

	require.NoError(t, err, "cleanup failure must not change the reconciliation result")
	assert.Contains(t, logs.String(), "failed to clean up rendered compose file", "cleanup failure must be logged")
}
