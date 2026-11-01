package stackloop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	downward "github.com/swarm-deploy/downward/go"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileUpdatesStateOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	require.NoError(t, writeComposeFile(repoDir), "write compose")
	otherRenderedPath := filepath.Join(repoDir, ".data", "rendered", "other.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(otherRenderedPath), 0o755), "create rendered dir")
	require.NoError(t, os.WriteFile(otherRenderedPath, []byte("other stack"), 0o600),
		"write another stack rendered compose")

	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).
		Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		}, event: eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-1",
	})

	require.NoError(t, err, "reconcile")
	assert.NoFileExists(t, filepath.Join(repoDir, ".data", "rendered", "app.yaml"),
		"rendered compose must be removed after successful reconciliation")
	assert.FileExists(t, otherRenderedPath, "reconciliation must not remove another stack rendered compose")
	state := readSQLState(t, stateStore)
	stackState, exists := state.Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-1", stackState.LastCommit, "unexpected last commit")
	assert.Empty(t, stackState.LastError, "expected empty error")
	assert.NotEmpty(t, stackState.SourceDigest, "expected stored source digest")
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
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	errDeployFailed := errors.New("deploy failed")
	eventDispatcher := &dispatcher.NopDispatcher{}

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	require.NoError(t, writeComposeFile(repoDir), "write compose")

	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).
		Return(deployer.Result{}, errDeployFailed)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		}, event: eventDispatcher,
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-2",
	})

	require.Error(t, err, "expected reconcile error")
	assert.ErrorIs(t, err, errDeployFailed, "unexpected error")
	assert.NoFileExists(t, filepath.Join(repoDir, ".data", "rendered", "app.yaml"),
		"rendered compose must be removed after failed deployment")
	state := readSQLState(t, stateStore)
	stackState, exists := state.Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-2", stackState.LastCommit, "unexpected last commit")
	assert.Equal(t, "apply_failed", stackState.LastError, "persist only a stable category")
	assert.Empty(t, stackState.SourceDigest, "expected empty source digest")
	require.Len(t, stackState.Services, 1, "expected one service state")
	serviceState := stackState.Services["api"]
	assert.Equal(t, model.SyncStatus(model.SyncStatusOutOfSync), serviceState.SyncStatus, "unexpected sync status")
	page, listErr := deploymentstore.NewStore(db).List(context.Background(), deploymentmodel.ListFilter{})
	require.NoError(t, listErr)
	require.Len(t, page.Deployments, 1)
	attempt, getErr := deploymentstore.NewStore(db).Get(context.Background(), page.Deployments[0].ID)
	require.NoError(t, getErr)
	assert.Equal(t, deploymentmodel.Failed, attempt.Status)
	assert.Equal(t, deploymentmodel.StageFailed, attempt.ApplyStatus)
	assert.Equal(t, deploymentmodel.StageSkipped, attempt.VerificationStatus)
	assert.Equal(t, deploymentmodel.ActualStateUnknown, attempt.ActualStateStatus)
}

func TestReconcileSeparatesPostApplyOutcomes(t *testing.T) {
	tests := []struct {
		name                 string
		setupServices        func(*swarm.MockServiceManager)
		status               deploymentmodel.Status
		phase                deploymentmodel.Phase
		verification         deploymentmodel.StageStatus
		cleanup              deploymentmodel.StageStatus
		actual               deploymentmodel.ActualStateStatus
		expectedObservedTime bool
		expectedError        bool
	}{
		{
			name: "verification unknown",
			setupServices: func(manager *swarm.MockServiceManager) {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, assert.AnError)
			},
			status: deploymentmodel.Interrupted, phase: deploymentmodel.PhaseVerification,
			verification: deploymentmodel.StageUnknown, cleanup: deploymentmodel.StageSkipped,
			actual: deploymentmodel.ActualStateUnknown, expectedError: true,
		},
		{
			name: "prune failure completes cleanup as failed",
			setupServices: func(manager *swarm.MockServiceManager) {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{{
					ID: "old-id", Name: "old", Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
					},
				}}, nil)
				manager.EXPECT().Remove(gomock.Any(), "old-id").Return(assert.AnError)
			},
			status: deploymentmodel.Failed, phase: deploymentmodel.PhaseCleanup,
			verification: deploymentmodel.StageSkipped, cleanup: deploymentmodel.StageFailed,
			actual: deploymentmodel.ActualStateObserved, expectedObservedTime: true, expectedError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			db, stateStore := newSQLState(t)
			repoDir := t.TempDir()
			require.NoError(t, writeComposeFile(repoDir))
			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
			tt.setupServices(serviceManager)
			cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(repoDir, ".data"),
				Sync: config.SyncSpec{Policy: config.SyncPolicySpec{Prune: true}}}}
			eventDispatcher := &dispatcher.NopDispatcher{}
			reconciler := &Reconciler{
				cfg: cfg, event: eventDispatcher,
				deployMetrics: &metrics.NopDeploys{}, stateStore: stateStore, pruner: pruner.NewServicePruner(serviceManager, eventDispatcher, cfg.Spec.Sync.Policy),
			}
			reconciler.db = db
			reconciler.executor = deployments.NewController(
				deployments.NewService(db, reconciler.event), stackDeployer,
				serviceManager, reconciler.pruner, reconciler.deployMetrics,
			)
			reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
			reconciler.attachPipeline()
			err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "commit",
			})
			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			page, listErr := deploymentstore.NewStore(db).List(context.Background(), deploymentmodel.ListFilter{})
			require.NoError(t, listErr)
			require.Len(t, page.Deployments, 1)
			attempt, getErr := deploymentstore.NewStore(db).Get(context.Background(), page.Deployments[0].ID)
			require.NoError(t, getErr)
			assert.Equal(t, tt.status, attempt.Status)
			assert.Equal(t, tt.phase, attempt.Phase)
			assert.Equal(t, deploymentmodel.StageSucceeded, attempt.ApplyStatus)
			assert.Equal(t, tt.verification, attempt.VerificationStatus)
			assert.Equal(t, tt.cleanup, attempt.CleanupStatus)
			assert.Equal(t, tt.actual, attempt.ActualStateStatus)
			assert.Equal(t, tt.expectedObservedTime, attempt.ObservedAt != nil)
		})
	}
}

func TestReconcileSucceedsWhenRotatedResourceCleanupPartiallyFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	secretManager := swarm.NewMockSecretManager(ctrl)
	configManager := swarm.NewMockConfigManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	composePath := filepath.Join(repoDir, "app.yaml")
	secretPath := filepath.Join(repoDir, "token")
	now := time.Now()
	errInUse := errors.New("resource is in use")

	require.NoError(t, os.WriteFile(secretPath, []byte("current-token"), 0o600), "write secret")
	require.NoError(t, os.WriteFile(composePath, []byte(`
services:
  api:
    image: nginx:latest
secrets:
  token:
    file: ./token
`), 0o600), "write compose")

	currentName := NewRotator(fs.NewLocalFileSystem()).buildRotatedObjectName("app", "token", "./token", []byte("current-token"), 8, false)
	liveSecrets := []swarm.Secret{
		managedSecret("old", "app-token-old", "token", now.Add(-2*time.Hour)),
		managedSecret("current", currentName, "token", now.Add(-time.Hour)),
	}

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", composePath, filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
	configManager.EXPECT().List(gomock.Any(), swarm.ListConfigsFilter{
		StackName: "app",
		Labels: map[string]string{
			labelsdict.RotatedResourceManagedLabelKey: labelsdict.RotatedResourceManagedLabelValue,
		},
	}).Return(nil, nil)
	secretManager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{
		StackName: "app",
		Labels: map[string]string{
			labelsdict.RotatedResourceManagedLabelKey: labelsdict.RotatedResourceManagedLabelValue,
		},
	}).Return(liveSecrets, nil)
	secretManager.EXPECT().Remove(gomock.Any(), "old").Return(errInUse)

	cfg := &config.Config{Spec: config.Spec{
		DataDir: filepath.Join(repoDir, ".data"),
		SecretRotation: config.SecretRotationSpec{
			Enabled:    true,
			HashLength: 8,
			Cleanup: config.SecretRotationCleanupSpec{
				Enabled:  true,
				KeepLast: 1,
				MinAge:   specw.Duration{Value: time.Hour},
			},
		},
	}}
	cleaner := newRotatedResourceCleaner(secretManager, configManager, cfg.Spec.SecretRotation.Cleanup)
	cleaner.now = func() time.Time { return now }
	reconciler := &Reconciler{
		cfg: cfg, event: &dispatcher.NopDispatcher{},
		deployMetrics:   &metrics.NopDeploys{},
		stateStore:      stateStore,
		pruner:          pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeRotator:  NewRotator(fs.NewLocalFileSystem()),
		driftAnalyzer:   drift.NewAnalyzer(),
		serviceManager:  serviceManager,
		secretManager:   secretManager,
		configManager:   configManager,
		resourceCleaner: cleaner,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack:  config.StackSpec{Name: "app", ComposeFile: "app.yaml"},
		Commit: "commit-cleanup",
	})

	require.NoError(t, err, "cleanup failure must not fail reconciliation")
	stackState, exists := readSQLState(t, stateStore).Stacks["app"]
	require.True(t, exists, "expected successful stack state")
	assert.Empty(t, stackState.LastError, "expected no reconciliation error")
}

func TestReconcileReadsPreviousDigestFromStateStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	secretManager := swarm.NewMockSecretManager(ctrl)
	configManager := swarm.NewMockConfigManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	require.NoError(t, writeComposeFile(repoDir), "write compose")

	loader := compose.NewFileLoader()
	stackFile, err := loader.Load(context.Background(), filepath.Join(repoDir, "app.yaml"))
	require.NoError(t, err, "load compose for digest")

	stateStore.Update(context.Background(), func(state *model.Runtime) {
		state.Stacks["app"] = model.Stack{
			SourceDigest: stackFile.Digest,
			LastCommit:   "previous-commit",
		}
	})

	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				SecretRotation: config.SecretRotationSpec{
					Enabled: true,
					Cleanup: config.SecretRotationCleanupSpec{
						Enabled: true,
					},
				},
			},
		}, event: eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
		secretManager:  secretManager,
		configManager:  configManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-3",
	})

	require.NoError(t, reconcileErr, "reconcile")
	stackState, exists := readSQLState(t, stateStore).Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, stackFile.Digest, stackState.SourceDigest, "expected persisted digest to remain unchanged")
}

func TestLiveServicesAfterPruneDropsPrunedServiceReferences(t *testing.T) {
	services := []swarm.StackService{
		{Name: "api", FullName: "app-api"},
		{Name: "worker", FullName: "app-worker"},
	}

	filtered := liveServicesAfterPrune(services, []string{"worker"})

	require.Len(t, filtered, 1)
	assert.Equal(t, "api", filtered[0].Name)
}

func TestReconcileDeploysRotatedConfigWhenConfigFileContentChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	configPath := filepath.Join(repoDir, "config", "app.yaml")
	composePath := filepath.Join(repoDir, "app.yaml")
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755), "create config dir")
	require.NoError(t, os.WriteFile(composePath, []byte(`
services:
  api:
    image: nginx:latest
    configs:
      - source: app-config
        target: /etc/app/config.yaml
configs:
  app-config:
    file: ./config/app.yaml
`), 0o600), "write compose")
	require.NoError(t, os.WriteFile(configPath, []byte("version: old\n"), 0o600), "write old config")

	loader := compose.NewFileLoader()
	oldStackFile, err := loader.Load(context.Background(), composePath)
	require.NoError(t, err, "load compose with old config")

	stateStore.Update(context.Background(), func(state *model.Runtime) {
		state.Stacks["app"] = model.Stack{
			SourceDigest: oldStackFile.Digest,
			LastCommit:   "previous-commit",
		}
	})

	require.NoError(t, os.WriteFile(configPath, []byte("version: new\n"), 0o600), "write new config")

	newStackFile, err := loader.Load(context.Background(), composePath)
	require.NoError(t, err, "load compose with new config")
	require.NotEqual(t, oldStackFile.Digest, newStackFile.Digest, "expected config content to change digest")

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	var renderedCompose *compose.Compose
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", composePath, renderedPath, gomock.Any()).
		Do(func(_ context.Context, _, _, deployComposePath string, _ compose.Compose, _ ...deployer.StackOptions) {
			renderedRaw, readErr := os.ReadFile(deployComposePath)
			require.NoError(t, readErr, "read rendered compose during deploy")
			renderedCompose, readErr = compose.Parse(renderedRaw)
			require.NoError(t, readErr, "parse rendered compose during deploy")
		}).
		Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				SecretRotation: config.SecretRotationSpec{
					Enabled:    true,
					HashLength: 8,
				},
			},
		}, event: eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-4",
	})

	require.NoError(t, reconcileErr, "reconcile")
	require.NotNil(t, renderedCompose, "expected rendered compose during deploy")
	assert.NoFileExists(t, renderedPath, "rendered compose must be removed after reconciliation")

	expectedConfigName := NewRotator(fs.NewLocalFileSystem()).buildRotatedObjectName(
		"app",
		"app-config",
		"./config/app.yaml",
		[]byte("version: new\n"),
		8,
		false,
	)
	assert.Equal(t, expectedConfigName, renderedCompose.Configs["app-config"].Name, "rotated config name should use new file content")

	stackState, exists := readSQLState(t, stateStore).Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, newStackFile.Digest, stackState.SourceDigest, "expected persisted digest from new config content")
}

func TestReconcileWritesRenderedComposeWithObjectFilesResolvedFromSourceCompose(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	eventDispatcher := &dispatcher.NopDispatcher{}
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")
	absConfigPath := filepath.Join(repoDir, "absolute", "config.yaml")
	absSecretPath := filepath.Join(repoDir, "absolute", "source-secret")

	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "deploy"), 0o755), "create compose dir")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "docker-compose.yaml"), []byte(fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    configs:
      - source: app-config
    secrets:
      - source: app-secret
configs:
  app-config:
    file: ./config/app.yaml
  abs-config:
    file: %s
secrets:
  app-secret:
    file: secrets/password.txt
  abs-secret:
    file: %s
`, absConfigPath, absSecretPath)), 0o600), "write compose")
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "deploy", "config"), 0o755), "create config dir")
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "deploy", "secrets"), 0o755), "create secrets dir")
	require.NoError(t, os.MkdirAll(filepath.Dir(absConfigPath), 0o755), "create absolute object dir")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "config", "app.yaml"), []byte("version: current\n"), 0o600), "write config")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "secrets", "password.txt"), []byte("password\n"), 0o600), "write secret")
	require.NoError(t, os.WriteFile(absConfigPath, []byte("absolute config\n"), 0o600), "write absolute config")
	require.NoError(t, os.WriteFile(absSecretPath, []byte("absolute secret\n"), 0o600), "write absolute secret")

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	var renderedCompose *compose.Compose
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "deploy", "docker-compose.yaml"), renderedPath, gomock.Any()).
		Do(func(_ context.Context, _, _, deployComposePath string, _ compose.Compose, _ ...deployer.StackOptions) {
			renderedRaw, readErr := os.ReadFile(deployComposePath)
			require.NoError(t, readErr, "read rendered compose during deploy")
			renderedCompose, readErr = compose.Parse(renderedRaw)
			require.NoError(t, readErr, "parse rendered compose during deploy")
		}).
		Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		}, event: eventDispatcher,
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "deploy/docker-compose.yaml",
		},
		Commit: "commit-4",
	})

	require.NoError(t, reconcileErr, "reconcile")
	require.NotNil(t, renderedCompose, "expected rendered compose during deploy")
	assert.NoFileExists(t, renderedPath, "rendered compose must be removed after reconciliation")

	assert.Equal(
		t,
		filepath.Join(repoDir, "deploy", "config", "app.yaml"),
		renderedCompose.Configs["app-config"].File,
		"relative config file should be resolved from source compose dir",
	)
	assert.Equal(
		t,
		absConfigPath,
		renderedCompose.Configs["abs-config"].File,
		"absolute config file should be preserved",
	)
	assert.Equal(t, &compose.Secret{Alias: "app-secret", Name: "app_app-secret", External: true}, renderedCompose.Secrets["app-secret"])
	assert.Equal(t, &compose.Secret{Alias: "abs-secret", Name: "app_abs-secret", External: true}, renderedCompose.Secrets["abs-secret"])

}

func TestReconcilePopulatesEnvFilesIntoRenderedEnvironment(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")

	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "deploy"), 0o755), "create compose dir")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "default.env"), []byte("FOO=default\nBAR=default\n"), 0o600), "write default env")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "prod.env"), []byte("FOO=prod\nBAZ=prod\n"), 0o600), "write prod env")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "deploy", "compose.yaml"), []byte(`
services:
  api:
    image: nginx:latest
    env_file:
      - default.env
      - prod.env
    environment:
      FOO: explicit
`), 0o600), "write compose")

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	var renderedCompose *compose.Compose
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "deploy", "compose.yaml"), renderedPath, gomock.Any()).
		Do(func(_ context.Context, _, _, deployComposePath string, _ compose.Compose, _ ...deployer.StackOptions) {
			renderedRaw, readErr := os.ReadFile(deployComposePath)
			require.NoError(t, readErr, "read rendered compose during deploy")
			renderedCompose, readErr = compose.Parse(renderedRaw)
			require.NoError(t, readErr, "parse rendered compose during deploy")
		}).
		Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		}, event: &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "deploy/compose.yaml",
		},
		Commit: "commit-env-1",
	})
	require.NoError(t, reconcileErr, "reconcile")
	require.NotNil(t, renderedCompose, "expected rendered compose during deploy")
	assert.NoFileExists(t, renderedPath, "rendered compose must be removed after reconciliation")
	require.Len(t, renderedCompose.Services, 1, "expected one service")

	service := renderedCompose.Services[0]
	assert.Empty(t, service.EnvFiles, "env_file must be removed from rendered desired state")
	assert.Equal(t, map[string]string{
		"BAR": "default",
		"BAZ": "prod",
		"FOO": "explicit",
	}, service.Environment.Map)
}

func TestReconcileWritesRenderedComposeForDownwardEnabledStack(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"), []byte(fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    deploy:
      labels:
        %s: %q
`, labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue)), 0o600), "write compose")

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	var renderedCompose *compose.Compose
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), renderedPath, gomock.Any()).
		Do(func(_ context.Context, _, _, deployComposePath string, _ compose.Compose, _ ...deployer.StackOptions) {
			renderedRaw, readErr := os.ReadFile(deployComposePath)
			require.NoError(t, readErr, "read rendered compose during deploy")
			renderedCompose, readErr = compose.Parse(renderedRaw)
			require.NoError(t, readErr, "parse rendered compose during deploy")
		}).
		Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				Containers: config.ContainersSpec{
					Downward: &struct{}{},
				},
			},
		}, event: &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-downward-1",
	})

	require.NoError(t, reconcileErr, "reconcile")
	require.NotNil(t, renderedCompose, "expected rendered compose during deploy")
	assert.NoFileExists(t, renderedPath, "rendered compose must be removed after reconciliation")
	require.Len(t, renderedCompose.Services, 1, "expected one service")

	service := renderedCompose.Services[0]
	assert.Equal(t, "app", service.Environment.Map[downward.EnvStackName], "unexpected stack env")
	assert.Equal(t, "{{.Service.ID}}", service.Environment.Map[downward.EnvServiceID], "unexpected service id env")
	assert.Equal(t, "{{.Service.Name}}", service.Environment.Map[downward.EnvServiceName], "unexpected service name env")
	assert.Equal(t, "{{.Task.ID}}", service.Environment.Map[downward.EnvTaskID], "unexpected task id env")
	assert.Equal(t, "{{.Task.Name}}", service.Environment.Map[downward.EnvTaskName], "unexpected task name env")
	assert.Equal(t, "{{.Task.Slot}}", service.Environment.Map[downward.EnvTaskSlot], "unexpected task slot env")
	assert.Equal(t, "{{.Node.ID}}", service.Environment.Map[downward.EnvNodeID], "unexpected node id env")
	assert.Equal(t, "{{.Node.Hostname}}", service.Environment.Map[downward.EnvNodeName], "unexpected node name env")
}

func TestReconcileDoesNotRedeployForDownwardOnUnchangedDigest(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	renderedPath := filepath.Join(repoDir, ".data", "rendered", "app.yaml")

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"), []byte(fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    deploy:
      labels:
        %s: %q
`, labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue)), 0o600), "write compose")

	repository.EXPECT().WorkingDir().Return(repoDir).Times(3)
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), renderedPath, gomock.Any()).
		Return(deployer.Result{}, nil).
		Times(1)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{
		{Name: "api"},
	}, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				Containers: config.ContainersSpec{
					Downward: &struct{}{},
				},
			},
		}, event: &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeRotator: NewRotator(fs.NewLocalFileSystem()),
		driftAnalyzer:  drift.NewAnalyzer(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-downward-2",
	})
	require.NoError(t, reconcileErr, "first reconcile")

	reconcileErr = reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-downward-3",
	})
	require.NoError(t, reconcileErr, "second reconcile")
}

func TestReconcilePrunesServicesWithoutNewDeployment(t *testing.T) {
	pruneEnabled := true
	for _, tc := range []struct {
		name        string
		manual      bool
		globalPrune bool
		stackPrune  *bool
	}{
		{name: "manual sync", manual: true, globalPrune: true},
		{name: "periodic sync with global policy", globalPrune: true},
		{name: "periodic sync with stack policy", stackPrune: &pruneEnabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			db, stateStore := newSQLState(t)
			repoDir := t.TempDir()
			eventDispatcher := &dispatcher.NopDispatcher{}
			deployMetrics := &metrics.NopDeploys{}

			require.NoError(t, writeComposeFile(repoDir), "write compose")

			loader := compose.NewFileLoader()
			stackFile, err := loader.Load(context.Background(), filepath.Join(repoDir, "app.yaml"))
			require.NoError(t, err, "load compose for digest")

			stateStore.Update(context.Background(), func(state *model.Runtime) {
				state.Stacks["app"] = model.Stack{
					SourceDigest: stackFile.Digest,
					LastCommit:   "previous-commit",
				}
			})

			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{
				{
					ID:   "service-api",
					Name: "api",
				},
				{
					ID:   "service-old",
					Name: "old",
					Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue,
					},
				},
			}, nil)
			serviceManager.EXPECT().Remove(gomock.Any(), "service-old").Return(nil)

			reconciler := &Reconciler{
				cfg: &config.Config{
					Spec: config.Spec{
						DataDir: filepath.Join(repoDir, ".data"),
						Sync: config.SyncSpec{
							Policy: config.SyncPolicySpec{
								Prune: tc.globalPrune,
							},
						},
					},
				}, event: eventDispatcher,
				deployMetrics:  deployMetrics,
				stateStore:     stateStore,
				pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{Prune: tc.globalPrune}),
				composeRotator: NewRotator(fs.NewLocalFileSystem()),
				serviceManager: serviceManager,
			}
			reconciler.db = db
			reconciler.executor = deployments.NewController(
				deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
			)
			reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
			reconciler.attachPipeline()

			reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{
					Name:        "app",
					ComposeFile: "app.yaml",
					Sync:        config.StackSyncSpec{Policy: config.StackSyncPolicySpec{Prune: tc.stackPrune}},
				},
				Commit:   "commit-4",
				IsManual: tc.manual,
			})

			require.NoError(t, reconcileErr, "reconcile")
			stackState, exists := readSQLState(t, stateStore).Stacks["app"]
			require.True(t, exists, "expected stack state")
			assert.Equal(t, stackFile.Digest, stackState.SourceDigest, "expected persisted digest to remain unchanged")
		})
	}
}

func TestReconcileServiceMissedEventOnDrift(t *testing.T) {
	tests := []struct {
		name                string
		liveServices        []swarm.StackService
		expectedEventsCount int
		expectedEvent       *events.ServiceMissed
		expectedSyncStatus  model.SyncStatus
		expectedSyncError   string
	}{
		{
			name:                "dispatches event when service is missing",
			liveServices:        nil,
			expectedEventsCount: 1,
			expectedEvent: &events.ServiceMissed{
				StackName:   "app",
				ServiceName: "api",
				Commit:      "commit-5",
			},
			expectedSyncStatus: model.SyncStatusOutOfSync,
			expectedSyncError:  "service is missing",
		},
		{
			name: "skips event when live state contains service",
			liveServices: []swarm.StackService{
				{Name: "api"},
			},
			expectedEventsCount: 0,
			expectedSyncStatus:  model.SyncStatusSynced,
			expectedSyncError:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			db, stateStore := newSQLState(t)
			repoDir := t.TempDir()
			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
			var captured []events.Event
			eventDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e events.Event) error { captured = append(captured, e); return nil }).AnyTimes()

			require.NoError(t, writeComposeFile(repoDir), "write compose")

			loader := compose.NewFileLoader()
			stackFile, err := loader.Load(context.Background(), filepath.Join(repoDir, "app.yaml"))
			require.NoError(t, err, "load compose for digest")

			stateStore.Update(context.Background(), func(state *model.Runtime) {
				state.Stacks["app"] = model.Stack{
					SourceDigest: stackFile.Digest,
					LastCommit:   "previous-commit",
				}
			})

			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(tt.liveServices, nil)

			reconciler := &Reconciler{
				cfg: &config.Config{
					Spec: config.Spec{
						DataDir: filepath.Join(repoDir, ".data"),
					},
				}, event: eventDispatcher,
				deployMetrics:  &metrics.NopDeploys{},
				stateStore:     stateStore,
				pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
				composeRotator: NewRotator(fs.NewLocalFileSystem()),
				driftAnalyzer:  drift.NewAnalyzer(),
				serviceManager: serviceManager,
			}
			reconciler.db = db
			reconciler.executor = deployments.NewController(
				deployments.NewService(db, reconciler.event), deployer.NewMockStackDeployer(ctrl),
				serviceManager, reconciler.pruner, reconciler.deployMetrics,
			)
			reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
			reconciler.attachPipeline()

			reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{
					Name:        "app",
					ComposeFile: "app.yaml",
				},
				Commit: "commit-5",
			})

			require.NoError(t, reconcileErr, "reconcile")
			require.Len(t, captured, tt.expectedEventsCount, "unexpected dispatched events count")

			if tt.expectedEvent != nil {
				missedEvent, ok := captured[0].(*events.ServiceMissed)
				require.True(t, ok, "expected service missed event")
				assert.Equal(t, tt.expectedEvent.StackName, missedEvent.StackName, "unexpected event stack")
				assert.Equal(t, tt.expectedEvent.ServiceName, missedEvent.ServiceName, "unexpected event service")
				assert.Equal(t, tt.expectedEvent.Commit, missedEvent.Commit, "unexpected event commit")
			}

			stackState, exists := readSQLState(t, stateStore).Stacks["app"]
			require.True(t, exists, "expected stack state")
			serviceState, exists := stackState.Services["api"]
			require.True(t, exists, "expected service state")
			assert.Equal(t, tt.expectedSyncStatus, serviceState.SyncStatus, "unexpected sync status")
			assert.Equal(t, tt.expectedSyncError, serviceState.SyncError, "unexpected sync error")
		})
	}
}

func TestReconcileSelfHealsMissingService(t *testing.T) {
	tests := []struct {
		name            string
		deployErr       error
		verificationErr error
		verifiedLive    []swarm.StackService
		wantErr         bool
		wantStatus      model.SyncStatus
		wantSyncError   string
		wantEventTypes  []events.TypeName
		wantMetric      string
	}{
		{
			name: "restores service", verifiedLive: []swarm.StackService{{Name: "api"}},
			wantStatus: model.SyncStatusSynced,
			wantEventTypes: []events.TypeName{
				events.TypeNameServiceMissed, events.TypeNameServiceDriftFixed,
			},
			wantMetric: "success",
		},
		{
			name: "CLI failure with verified convergence", deployErr: errors.New("docker CLI timeout"),
			verifiedLive: []swarm.StackService{{Name: "api"}}, wantStatus: model.SyncStatusSynced,
			wantEventTypes: []events.TypeName{
				events.TypeNameServiceMissed, events.TypeNameServiceDriftFixed,
			},
			wantMetric: "success",
		},
		{
			name: "records failed restore", deployErr: errors.New("deploy failed"), wantErr: true,
			wantStatus: model.SyncStatusOutOfSync, wantSyncError: "Self Heal Verification Failed: service is missing",
			wantEventTypes: []events.TypeName{events.TypeNameServiceMissed},
			wantMetric:     "failed",
		},
		{
			name: "records failed verification", verificationErr: errors.New("swarm unavailable"), wantErr: true,
			wantStatus: model.SyncStatusOutOfSync, wantSyncError: "Self Heal Verification Failed",
			wantEventTypes: []events.TypeName{events.TypeNameServiceMissed},
			wantMetric:     "failed",
		},
		{
			name: "records persistent drift", wantErr: true,
			wantStatus:     model.SyncStatusOutOfSync,
			wantSyncError:  "Self Heal Verification Failed: service is missing",
			wantEventTypes: []events.TypeName{events.TypeNameServiceMissed},
			wantMetric:     "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			db, stateStore := newSQLState(t)
			repoDir := t.TempDir()
			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
			var captured []events.TypeName
			eventDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					captured = append(captured, event.Type().Name())
					return nil
				},
			).AnyTimes()

			require.NoError(t, writeComposeFile(repoDir))
			loader := compose.NewFileLoader()
			stackFile, err := loader.Load(context.Background(), filepath.Join(repoDir, "app.yaml"))
			require.NoError(t, err)
			require.NoError(t, stateStore.Update(context.Background(), func(state *model.Runtime) {
				state.Stacks["app"] = model.Stack{SourceDigest: stackFile.Digest, LastCommit: "previous"}
			}))

			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
			serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(tt.verifiedLive, tt.verificationErr)
			stackDeployer.EXPECT().DeployService(
				gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), "api", gomock.Any(), gomock.Any(),
			).Return(deployer.Result{}, tt.deployErr)

			metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test"})
			registry := prometheus.NewRegistry()
			registry.MustRegister(metricGroup)
			reconciler := &Reconciler{
				cfg: &config.Config{Spec: config.Spec{
					DataDir: filepath.Join(repoDir, ".data"),
					Sync:    config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}},
				}}, event: eventDispatcher,
				deployMetrics: metricGroup.Deploys, stateStore: stateStore,
				pruner:        pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
				driftAnalyzer: drift.NewAnalyzer(), serviceManager: serviceManager,
			}
			reconciler.db = db
			reconciler.executor = deployments.NewController(
				deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
			)
			reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
			reconciler.attachPipeline()

			err = reconciler.Reconcile(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "current",
			})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			state := readSQLState(t, stateStore).Stacks["app"].Services["api"]
			assert.Equal(t, tt.wantStatus, state.SyncStatus)
			assert.Equal(t, tt.wantSyncError, state.SyncError)
			assert.Equal(t, tt.wantEventTypes, captured)
			assert.EqualValues(t, 1, deployMetricValue(t, registry, "app", "api", tt.wantMetric))
		})
	}
}

func deployMetricValue(t *testing.T, registry *prometheus.Registry, stack, service, status string) float64 {
	t.Helper()
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "test_deploys_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["stack"] == stack && labels["service"] == service && labels["status"] == status {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestReconcileSelfHealsReplicaDriftAfterVerification(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	repoDir := t.TempDir()
	eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
	var captured []events.Event
	eventDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event events.Event) error {
			captured = append(captured, event)
			return nil
		},
	).AnyTimes()

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"), []byte(`services:
  api:
    image: nginx:latest
    deploy:
      replicas: 3
`), 0o600))
	loader := compose.NewFileLoader()
	stackFile, err := loader.Load(context.Background(), filepath.Join(repoDir, "app.yaml"))
	require.NoError(t, err)
	require.NoError(t, stateStore.Update(context.Background(), func(state *model.Runtime) {
		state.Stacks["app"] = model.Stack{SourceDigest: stackFile.Digest, LastCommit: "previous"}
	}))

	one, three := uint64(1), uint64(3)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{{
		Name: "api", Replicas: &one,
	}}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{{
		Name: "api", Replicas: &three,
	}}, nil)
	stackDeployer.EXPECT().DeployService(
		gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), "api", gomock.Any(), gomock.Any(),
	).Return(deployer.Result{}, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{Spec: config.Spec{
			DataDir: filepath.Join(repoDir, ".data"),
			Sync:    config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}},
		}}, event: eventDispatcher,
		deployMetrics: &metrics.NopDeploys{}, stateStore: stateStore,
		pruner:        pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		driftAnalyzer: drift.NewAnalyzer(), serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.executor = deployments.NewController(
		deployments.NewService(db, reconciler.event), stackDeployer, serviceManager, reconciler.pruner, reconciler.deployMetrics,
	)
	reconciler.preparer = preparation.New(reconciler.cfg, repository, fs.NewLocalFileSystem(), reconciler.composeRotator)
	reconciler.attachPipeline()

	require.NoError(t, reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "current",
	}))
	state := readSQLState(t, stateStore).Stacks["app"].Services["api"]
	assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), state.SyncStatus)
	require.Len(t, captured, 2)
	drifted, ok := captured[0].(*events.ServiceDrifted)
	require.True(t, ok)
	assert.Equal(t, "replica count differs: desired=3 actual=1", drifted.Reason)
	assert.True(t, drifted.Resolved)
	assert.IsType(t, &events.ServiceDriftFixed{}, captured[1])
}

func TestPublishDriftEventsUsesEpisodesAndConfirmedResolution(t *testing.T) {
	tests := []struct {
		name      string
		previous  model.SyncStatus
		detected  map[string]drift.ServiceDrift
		remaining map[string]drift.ServiceDrift
		want      []events.TypeName
	}{
		{
			name: "unchanged unresolved drift is not duplicated", previous: model.SyncStatusOutOfSync,
			detected: map[string]drift.ServiceDrift{"api": {
				ServiceName: "api", ServiceMissed: true, Reason: "service is missing",
			}},
			remaining: map[string]drift.ServiceDrift{"api": {
				ServiceName: "api", ServiceMissed: true, Reason: "service is missing",
			}},
		},
		{
			name: "external or deployment recovery is fixed", previous: model.SyncStatusOutOfSync,
			want: []events.TypeName{events.TypeNameServiceDriftFixed},
		},
		{
			name: "new replica drift is detected", detected: map[string]drift.ServiceDrift{"api": {
				ServiceName: "api", Reason: "replicas differ", Replicas: drift.ReplicasDrift{OutOfSync: true},
			}}, remaining: map[string]drift.ServiceDrift{"api": {
				ServiceName: "api", Reason: "replicas differ", Replicas: drift.ReplicasDrift{OutOfSync: true},
			}}, want: []events.TypeName{events.TypeNameServiceDrifted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
			var published []events.TypeName
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					published = append(published, event.Type().Name())
					return nil
				},
			).AnyTimes()
			reconciler := &Reconciler{event: publisher}
			previous := model.Stack{Services: map[string]model.Service{
				"api": {SyncStatus: tt.previous},
			}}
			payload := &pipelinePayload{
				Desired: &compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api"}}}},
				Drift:   tt.detected, RemainingDrift: tt.remaining,
			}

			require.NoError(t, reconciler.publishDriftEvents(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app"}, Commit: "current",
			}, previous, payload))
			assert.Equal(t, tt.want, published)
		})
	}
}

func TestPublishDriftEventsUsesFinalVerifiedStatePerService(t *testing.T) {
	tests := []struct {
		name      string
		previous  map[string]model.Service
		initial   map[string]drift.ServiceDrift
		remaining map[string]drift.ServiceDrift
		want      []string
	}{
		{
			name: "repaired service while another becomes drifted",
			initial: map[string]drift.ServiceDrift{
				"api": {ServiceName: "api", ServiceMissed: true, Reason: "service is missing"},
			},
			remaining: map[string]drift.ServiceDrift{
				"worker": {ServiceName: "worker", Reason: "replicas differ"},
			},
			want: []string{"ServiceMissed:api:true", "ServiceDriftFixed:api", "ServiceDrifted:worker:false"},
		},
		{
			name: "repaired service while another disappears",
			initial: map[string]drift.ServiceDrift{
				"api": {ServiceName: "api", ServiceMissed: true, Reason: "service is missing"},
			},
			remaining: map[string]drift.ServiceDrift{
				"worker": {ServiceName: "worker", ServiceMissed: true, Reason: "service is missing"},
			},
			want: []string{"ServiceMissed:api:true", "ServiceDriftFixed:api", "ServiceMissed:worker:false"},
		},
		{
			name: "multiple services repaired",
			initial: map[string]drift.ServiceDrift{
				"api":    {ServiceName: "api", Reason: "replicas differ"},
				"worker": {ServiceName: "worker", ServiceMissed: true, Reason: "service is missing"},
			},
			want: []string{
				"ServiceDrifted:api:true", "ServiceDriftFixed:api",
				"ServiceMissed:worker:true", "ServiceDriftFixed:worker",
			},
		},
		{
			name: "one repaired while another remains drifted",
			initial: map[string]drift.ServiceDrift{
				"api":    {ServiceName: "api", Reason: "replicas differ"},
				"worker": {ServiceName: "worker", Reason: "replicas differ"},
			},
			remaining: map[string]drift.ServiceDrift{
				"worker": {ServiceName: "worker", Reason: "replicas differ"},
			},
			want: []string{
				"ServiceDrifted:api:true", "ServiceDriftFixed:api", "ServiceDrifted:worker:false",
			},
		},
		{
			name: "unchanged unresolved drift is not duplicated",
			previous: map[string]model.Service{
				"worker": {SyncStatus: model.SyncStatusOutOfSync},
			},
			initial: map[string]drift.ServiceDrift{
				"worker": {ServiceName: "worker", Reason: "replicas differ"},
			},
			remaining: map[string]drift.ServiceDrift{
				"worker": {ServiceName: "worker", Reason: "replicas differ"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
			var published []string
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					switch typed := event.(type) {
					case *events.ServiceMissed:
						published = append(published, fmt.Sprintf("ServiceMissed:%s:%t", typed.ServiceName, typed.Resolved))
					case *events.ServiceDrifted:
						published = append(published, fmt.Sprintf("ServiceDrifted:%s:%t", typed.ServiceName, typed.Resolved))
					case *events.ServiceDriftFixed:
						published = append(published, "ServiceDriftFixed:"+typed.ServiceName)
					}
					return nil
				},
			).AnyTimes()
			reconciler := &Reconciler{event: publisher}
			payload := &pipelinePayload{
				Desired: &compose.File{Compose: compose.Compose{Services: compose.Services{
					{Name: "api"}, {Name: "worker"},
				}}},
				Drift: tt.initial, RemainingDrift: tt.remaining,
			}
			require.NoError(t, reconciler.publishDriftEvents(t.Context(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app"}, Commit: "current",
			}, model.Stack{Services: tt.previous}, payload))
			assert.Equal(t, tt.want, published)
		})
	}
}

func newSQLState(t *testing.T) (storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, modelstore.NewSQLStore(db)
}
func readSQLState(t *testing.T, store *modelstore.SQLStore) model.Runtime {
	t.Helper()
	state, err := store.Read(context.Background())
	require.NoError(t, err)
	return state
}
func writeComposeFile(repoDir string) error {
	content := []byte("services:\n  api:\n    image: nginx:latest\n")
	return os.WriteFile(filepath.Join(repoDir, "app.yaml"), content, 0o600)
}
