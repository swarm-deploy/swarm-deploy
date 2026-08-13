package stackloop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller/stackloop/pruner"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
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
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	require.NoError(t, writeComposeFile(repoDir), "write compose")

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
		event:          eventDispatcher,
		deployMetrics:  deployMetrics,
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.attachPipeline()

	err := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-1",
	})

	require.NoError(t, err, "reconcile")
	state := stateStore.Get()
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
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()
	errDeployFailed := errors.New("deploy failed")
	eventDispatcher := &dispatcher.NopDispatcher{}

	require.NoError(t, writeComposeFile(repoDir), "write compose")

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
		event:          eventDispatcher,
		deployMetrics:  &metrics.NopDeploys{},
		stateStore:     stateStore,
		pruner:         pruner.NewServicePruner(serviceManager, eventDispatcher, config.SyncPolicySpec{}),
		composeLoader:  compose.NewFileLoader(),
		composeRotator: NewRotator(),
	}
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
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	require.NoError(t, writeComposeFile(repoDir), "write compose")

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
		composeLoader:  loader,
		composeRotator: NewRotator(),
		serviceManager: serviceManager,
	}
	reconciler.attachPipeline()

	reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
		Stack: config.StackSpec{
			Name:        "app",
			ComposeFile: "app.yaml",
		},
		Commit: "commit-3",
	})

	require.NoError(t, reconcileErr, "reconcile")
	stackState, exists := stateStore.Get().Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, stackFile.Digest, stackState.SourceDigest, "expected persisted digest to remain unchanged")
}

func TestReconcileDeploysRotatedConfigWhenConfigFileContentChanges(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
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
	oldStackFile, err := loader.Load(composePath)
	require.NoError(t, err, "load compose with old config")

	stateStore.Update(func(state *model.Runtime) {
		state.Stacks["app"] = model.Stack{
			SourceDigest: oldStackFile.Digest,
			LastCommit:   "previous-commit",
		}
	})

	require.NoError(t, os.WriteFile(configPath, []byte("version: new\n"), 0o600), "write new config")

	newStackFile, err := loader.Load(composePath)
	require.NoError(t, err, "load compose with new config")
	require.NotEqual(t, oldStackFile.Digest, newStackFile.Digest, "expected config content to change digest")

	repository.EXPECT().WorkingDir().Return(repoDir)
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", renderedPath, gomock.Any()).
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

	stackState, exists := stateStore.Get().Stacks["app"]
	require.True(t, exists, "expected stack state")
	assert.Equal(t, newStackFile.Digest, stackState.SourceDigest, "expected persisted digest from new config content")
}

func TestReconcileWritesRenderedComposeWithObjectFilesResolvedFromSourceCompose(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
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

	repository.EXPECT().WorkingDir().Return(repoDir)
	stackDeployer.EXPECT().
		DeployStack(gomock.Any(), "app", renderedPath, gomock.Any()).
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

func TestReconcilePrunesServicesForSkippedManualSync(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	stateStore := modelstore.NewMemoryStore()
	repoDir := t.TempDir()
	eventDispatcher := &dispatcher.NopDispatcher{}
	deployMetrics := &metrics.NopDeploys{}

	require.NoError(t, writeComposeFile(repoDir), "write compose")

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
	stackState, exists := stateStore.Get().Stacks["app"]
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
			stateStore := modelstore.NewMemoryStore()
			repoDir := t.TempDir()
			eventDispatcher := &captureEventDispatcher{}

			require.NoError(t, writeComposeFile(repoDir), "write compose")

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
			reconciler.attachPipeline()

			reconcileErr := reconciler.Reconcile(context.Background(), ReconciliationRequest{
				Stack: config.StackSpec{
					Name:        "app",
					ComposeFile: "app.yaml",
				},
				Commit: "commit-5",
			})

			require.NoError(t, reconcileErr, "reconcile")
			require.Len(t, eventDispatcher.events, tt.expectedEventsCount, "unexpected dispatched events count")

			if tt.expectedEvent != nil {
				missedEvent, ok := eventDispatcher.events[0].(*events.ServiceMissed)
				require.True(t, ok, "expected service missed event")
				assert.Equal(t, tt.expectedEvent.StackName, missedEvent.StackName, "unexpected event stack")
				assert.Equal(t, tt.expectedEvent.ServiceName, missedEvent.ServiceName, "unexpected event service")
				assert.Equal(t, tt.expectedEvent.Commit, missedEvent.Commit, "unexpected event commit")
			}

			stackState, exists := stateStore.Get().Stacks["app"]
			require.True(t, exists, "expected stack state")
			serviceState, exists := stackState.Services["api"]
			require.True(t, exists, "expected service state")
			assert.Equal(t, tt.expectedSyncStatus, serviceState.SyncStatus, "unexpected sync status")
			assert.Equal(t, tt.expectedSyncError, serviceState.SyncError, "unexpected sync error")
		})
	}
}

type captureEventDispatcher struct {
	events []events.Event
}

func (d *captureEventDispatcher) Subscribe(_ events.Type, _ dispatcher.Subscriber) {}

func (d *captureEventDispatcher) Dispatch(_ context.Context, event events.Event) {
	d.events = append(d.events, event)
}

func (d *captureEventDispatcher) Shutdown(_ context.Context) error {
	return nil
}

func writeComposeFile(repoDir string) error {
	content := []byte("services:\n  api:\n    image: nginx:latest\n")
	return os.WriteFile(filepath.Join(repoDir, "app.yaml"), content, 0o600)
}
