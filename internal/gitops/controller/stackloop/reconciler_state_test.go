package stackloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileUpdatesStateOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()

	require.NoError(t, writeComposeFile(repoDir, "app.yaml"), "write compose")

	repository.EXPECT().WorkingDir().Return(repoDir)
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).
		Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		stateStore:     stateStore,
		pruner:         NewServicePruner(serviceManager, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
	}
	reconciler.attachPipeline()

	result, err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-1",
	})

	require.NoError(t, err, "reconcile")
	assert.False(t, result.Skipped, "expected deploy")

	state := stateStore.Get()
	stackState, exists := state.Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-1", stackState.LastCommit, "unexpected last commit")
	assert.Empty(t, stackState.LastError, "expected empty error")
	assert.Equal(t, result.SourceDigest, stackState.SourceDigest, "unexpected source digest")
	require.Len(t, stackState.Services, 1, "expected one service state")
	serviceState := stackState.Services["api"]
	assert.Equal(t, "nginx:latest", serviceState.Image, "unexpected image")
	assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), serviceState.SyncStatus, "unexpected sync status")
}

func TestReconcileUpdatesStateOnFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()
	errDeployFailed := errors.New("deploy failed")

	require.NoError(t, writeComposeFile(repoDir, "app.yaml"), "write compose")

	repository.EXPECT().WorkingDir().Return(repoDir)
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).
		Return(errDeployFailed)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		stateStore:     stateStore,
		pruner:         NewServicePruner(serviceManager, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
	}
	reconciler.attachPipeline()

	_, err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-2",
	})

	require.Error(t, err, "expected reconcile error")
	assert.ErrorIs(t, err, errDeployFailed, "unexpected error")

	state := stateStore.Get()
	stackState, exists := state.Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-2", stackState.LastCommit, "unexpected last commit")
	assert.Contains(t, stackState.LastError, errDeployFailed.Error(), "unexpected last error")
	assert.Empty(t, stackState.SourceDigest, "expected empty source digest")
	require.Len(t, stackState.Services, 1, "expected one service state")
	serviceState := stackState.Services["api"]
	assert.Equal(t, model.SyncStatus(model.SyncStatusOutOfSync), serviceState.SyncStatus, "unexpected sync status")
}

func TestReconcileReadsPreviousDigestFromStateStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()

	require.NoError(t, writeComposeFile(repoDir, "app.yaml"), "write compose")

	loader := compose.NewFileLoader()
	stackFile, err := loader.Load(filepath.Join(repoDir, "app.yaml"))
	require.NoError(t, err, "load compose for digest")

	stateStore.Update(func(state *model.Runtime) {
		state.Stacks["app"] = model.Stack{
			SourceDigest: stackFile.Digest,
			LastCommit:   "previous-commit",
		}
	})

	repository.EXPECT().WorkingDir().Return(repoDir)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		stateStore:     stateStore,
		pruner:         NewServicePruner(swarm.NewMockServiceManager(ctrl), config.SyncPolicySpec{}),
		composeLoader:  loader,
		composeRotator: NewRotator(),
	}
	reconciler.attachPipeline()

	result, reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-3",
	})

	require.NoError(t, reconcileErr, "reconcile")
	assert.True(t, result.Skipped, "expected skip from persisted digest")
}

func writeComposeFile(repoDir string, composePath string) error {
	content := []byte("services:\n  api:\n    image: nginx:latest\n")
	return os.WriteFile(filepath.Join(repoDir, composePath), content, 0o600)
}
