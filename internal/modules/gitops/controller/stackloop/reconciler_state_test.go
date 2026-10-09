package stackloop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	downward "github.com/swarm-deploy/downward/go"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
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

	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).
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
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-1",
	})

	require.NoError(t, err, "reconcile")
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
		Return(errDeployFailed)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          eventDispatcher,
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
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
	state := readSQLState(t, stateStore)
	stackState, exists := state.Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-2", stackState.LastCommit, "unexpected last commit")
	assert.Equal(t, "apply_failed", stackState.LastError, "persist only a stable category")
	assert.Empty(t, stackState.SourceDigest, "expected empty source digest")
	require.Len(t, stackState.Services, 1, "expected one service state")
	serviceState := stackState.Services["api"]
	assert.Equal(t, model.SyncStatus(model.SyncStatusOutOfSync), serviceState.SyncStatus, "unexpected sync status")
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

	currentName := NewRotator().buildRotatedObjectName("app", "token", "./token", []byte("current-token"), 8, false)
	liveSecrets := []swarm.Secret{
		managedSecret("old", "app-token-old", "token", now.Add(-2*time.Hour)),
		managedSecret("current", currentName, "token", now.Add(-time.Hour)),
	}

	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", composePath, filepath.Join(repoDir, ".data", "rendered", "app.yaml"), gomock.Any()).Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)
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
		cfg:             cfg,
		git:             repository,
		deployer:        stackDeployer,
		event:           &dispatcher.NopDispatcher{},
		deployMetrics:   &metrics.NopDeploys{},
		stateStore:      stateStore,
		pruner:          pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:   compose.NewFileLoader(),
		composeRotator:  NewRotator(),
		driftAnalyzer:   drift.NewAnalyzer(),
		serviceManager:  serviceManager,
		secretManager:   secretManager,
		configManager:   configManager,
		resourceCleaner: cleaner,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
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

	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

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
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  loader,
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
		secretManager:  secretManager,
		configManager:  configManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
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
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", composePath, renderedPath, gomock.Any()).
		Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				SecretRotation: config.SecretRotationSpec{
					Enabled:    true,
					HashLength: 8,
				},
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  loader,
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-4",
	})

	require.NoError(t, reconcileErr, "reconcile")

	renderedRaw, err := os.ReadFile(renderedPath)
	require.NoError(t, err, "read rendered compose")

	renderedCompose, err := compose.Parse(renderedRaw)
	require.NoError(t, err, "parse rendered compose")

	expectedConfigName := NewRotator().buildRotatedObjectName(
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
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "deploy", "docker-compose.yaml"), renderedPath, gomock.Any()).
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
		event:          eventDispatcher,
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "deploy/docker-compose.yaml",
		},
		Commit: "commit-4",
	})

	require.NoError(t, reconcileErr, "reconcile")

	renderedRaw, err := os.ReadFile(renderedPath)
	require.NoError(t, err, "read rendered compose")

	renderedCompose, err := compose.Parse(renderedRaw)
	require.NoError(t, err, "parse rendered compose")

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
	assert.Equal(
		t,
		filepath.Join(repoDir, "deploy", "secrets", "password.txt"),
		renderedCompose.Secrets["app-secret"].File,
		"relative secret file should be resolved from source compose dir",
	)
	assert.Equal(
		t,
		absSecretPath,
		renderedCompose.Secrets["abs-secret"].File,
		"absolute secret file should be preserved",
	)
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
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "deploy", "compose.yaml"), renderedPath, gomock.Any()).
		Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

	readFile := func(_ context.Context, path string) ([]byte, error) {
		return os.ReadFile(path)
	}
	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
			},
		},
		git:              repository,
		deployer:         stackDeployer,
		event:            &dispatcher.NopDispatcher{},
		deployMetrics:    &metrics.NopDeploys{},
		stateStore:       stateStore,
		pruner:           pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:    compose.NewFileLoaderWithReader(readFile),
		envFilePopulator: compose.NewEnvFilePopulator(),
		composeRotator:   NewRotator(),
		serviceManager:   serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "deploy/compose.yaml",
		},
		Commit: "commit-env-1",
	})
	require.NoError(t, reconcileErr, "reconcile")

	renderedRaw, err := os.ReadFile(renderedPath)
	require.NoError(t, err, "read rendered compose")

	renderedCompose, err := compose.Parse(renderedRaw)
	require.NoError(t, err, "parse rendered compose")
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
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", filepath.Join(repoDir, "app.yaml"), renderedPath, gomock.Any()).
		Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)

	reconciler := &Reconciler{
		cfg: &config.Config{
			Spec: config.Spec{
				DataDir: filepath.Join(repoDir, ".data"),
				Containers: config.ContainersSpec{
					Downward: &struct{}{},
				},
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-downward-1",
	})

	require.NoError(t, reconcileErr, "reconcile")

	renderedRaw, err := os.ReadFile(renderedPath)
	require.NoError(t, err, "read rendered compose")

	renderedCompose, err := compose.Parse(renderedRaw)
	require.NoError(t, err, "parse rendered compose")
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
		Return(nil).
		Times(1)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{
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
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          &dispatcher.NopDispatcher{},
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, &dispatcher.NopDispatcher{}, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		driftAnalyzer:  drift.NewAnalyzer(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
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

func TestAddDownwardPreservesExistingVariableAndAddsMissingOnes(t *testing.T) {
	reconciler := &Reconciler{}
	payload := &pipelinePayload{
		Stack: config.StackSpec{
			Name: "app",
		},
		IsNewDigest: true,
		Desired: &compose.File{
			Compose: compose.Compose{
				Services: compose.Services{
					{
						Name:  "api",
						Image: "nginx:latest",
						Environment: compose.Environment{
							Map: map[string]string{
								downward.EnvServiceName: "custom-service",
							},
						},
					},
				},
			},
		},
	}

	err := reconciler.addDownward(context.Background(), payload)

	require.NoError(t, err, "add downward")
	assert.True(t, payload.DesiredMutated, "expected missing downward variables to mutate desired state")

	environment := payload.Desired.Compose.Services[0].Environment.Map
	assert.Equal(t, "custom-service", environment[downward.EnvServiceName], "explicit downward value must win")
	assert.Equal(t, "app", environment[downward.EnvStackName], "expected stack name")
	assert.Equal(t, "{{.Task.ID}}", environment[downward.EnvTaskID], "expected missing downward variable")
}

func TestReconcilePrunesServicesForSkippedManualSync(t *testing.T) {
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
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{
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
						Prune: true,
					},
				},
			},
		},
		git:            repository,
		deployer:       stackDeployer,
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{Prune: true}),
		composeLoader:  loader,
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.db = db
	reconciler.deployments = deployment.NewService(db, reconciler.event)
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit:   "commit-4",
		IsManual: true,
	})

	require.NoError(t, reconcileErr, "reconcile")
	stackState, exists := readSQLState(t, stateStore).Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, stackFile.Digest, stackState.SourceDigest, "expected persisted digest to remain unchanged")
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
			expectedSyncError:  "Service Missed",
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
			serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(tt.liveServices, nil)

			reconciler := &Reconciler{
				cfg: &config.Config{
					Spec: config.Spec{
						DataDir: filepath.Join(repoDir, ".data"),
					},
				},
				git:            repository,
				event:          eventDispatcher,
				deployMetrics:  &metrics.NopDeploys{},
				stateStore:     stateStore,
				pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
				composeLoader:  loader,
				composeRotator: NewRotator(),
				driftAnalyzer:  drift.NewAnalyzer(),
				serviceManager: serviceManager,
			}
			reconciler.db = db
			reconciler.deployments = deployment.NewService(db, reconciler.event)
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

func newSQLState(t *testing.T) (*storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
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
