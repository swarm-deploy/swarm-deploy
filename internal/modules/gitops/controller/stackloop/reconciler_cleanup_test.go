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
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileRemovesRenderedComposeAfterEarlyPipelineFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")
	errAfterRender := errors.New("failed after render")

	require.NoError(t, writeComposeFile(repoDir), "write compose")
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	reconciler := &Reconciler{
		cfg:              &config.Config{Spec: config.Spec{DataDir: filepath.Join(repoDir, ".data")}},
		git:              repository,
		event:            &dispatcher.NopDispatcher{},
		deployMetrics:    &metrics.NopDeploys{},
		stateStore:       stateStore,
		composeLoader:    compose.NewFileLoader(),
		composeRotator:   NewRotator(),
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
}

func TestNewReconcilerRemovesStaleRenderedComposeWithoutRemovingSource(t *testing.T) {
	repoDir := t.TempDir()
	renderedDir := filepath.Join(repoDir, ".data", "rendered")
	sourcePath := filepath.Join(repoDir, "source.yaml")

	require.NoError(t, os.MkdirAll(renderedDir, 0o755), "create rendered dir")
	require.NoError(t, os.WriteFile(filepath.Join(renderedDir, "current.yaml"), []byte("sensitive"), 0o600),
		"write stale rendered compose")
	require.NoError(t, os.WriteFile(filepath.Join(renderedDir, "removed-stack.yaml"), []byte("sensitive"), 0o600),
		"write stale rendered compose for removed stack")
	require.NoError(t, os.WriteFile(sourcePath, []byte("services: {}\n"), 0o600), "write source compose")

	New(
		&config.Config{Spec: config.Spec{DataDir: filepath.Join(repoDir, ".data")}},
		nil,
		nil,
		&swarm.Swarm{},
		nil,
		nil,
		nil,
		fs.NewLocalFileSystem(),
		nil,
	)

	assert.NoDirExists(t, renderedDir, "all stale rendered compose files must be removed")
	assert.FileExists(t, sourcePath, "source compose from Git must be preserved")
}

func TestRenderedComposeCleanupFailureDoesNotFailReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	dataDir := filepath.Join(repoDir, ".data-file")
	composePath := filepath.Join(repoDir, "app.yaml")

	require.NoError(t, os.WriteFile(dataDir, []byte("not a directory"), 0o600), "write data dir blocker")
	require.NoError(t, os.WriteFile(composePath, []byte("services:\n  api:\n    image: nginx:latest\n    deploy:\n      labels:\n        "+labelsdict.ServiceManagedLabelKey+": \""+labelsdict.ServiceManagedLabelValue+"\"\n"), 0o600), "write compose")
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", composePath, composePath, gomock.Any()).Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

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
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
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
	assert.Contains(t, logs.String(), "failed to clean up rendered compose files", "cleanup failure must be logged")
}
