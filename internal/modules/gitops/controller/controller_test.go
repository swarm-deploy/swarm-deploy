package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	git "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.uber.org/mock/gomock"
)

func TestControllerGracefulShutdown(t *testing.T) {
	testCases := []struct {
		name string
		mode string
	}{
		{
			name: "pull mode",
			mode: config.SyncModePull,
		},
		{
			name: "hybrid mode",
			mode: config.SyncModeHybrid,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			started := make(chan context.Context, 1)
			release := make(chan struct{})

			repository := git.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).DoAndReturn(func(ctx context.Context) (git.PullResult, error) {
				started <- ctx
				<-release
				return git.PullResult{}, errors.New("pull failed")
			}).Times(1)

			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)

			metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
				Namespace: "test_controller_graceful_shutdown",
			})
			controller := &Controller{
				cfg: &config.Config{Spec: config.Spec{
					Git: config.GitSpec{Repository: "repo"},
					Sync: config.SyncSpec{
						Mode:         testCase.mode,
						PollInterval: specw.Duration{Value: 5 * time.Millisecond},
					},
				}},
				git:        repository,
				metrics:    metricGroup,
				event:      eventDispatcher,
				stateStore: modelstore.NewMemoryStore(),
				triggerCh:  make(chan triggerTask, 1),
				tracer:     otel.Tracer("test"),
			}

			runCtx, cancelRun := context.WithCancel(context.Background())
			defer cancelRun()
			runResult := make(chan error, 1)
			go func() {
				runResult <- controller.Run(runCtx)
			}()

			reconciliationCtx := <-started
			cancelRun()

			require.Eventually(t, controller.shuttingDown.Load, time.Second, time.Millisecond,
				"controller should enter shutdown state")
			assert.False(t, controller.Manual(context.Background()), "manual sync must be rejected during shutdown")
			assert.NoError(t, reconciliationCtx.Err(), "active reconciliation context must remain usable")

			select {
			case err := <-runResult:
				require.FailNow(t, "controller stopped before reconciliation completed", "error: %v", err)
			case <-time.After(20 * time.Millisecond):
			}

			close(release)

			require.NoError(t, <-runResult, "run controller")
		})
	}
}

func TestReloadStacksUsesRepositoryDirFirst(t *testing.T) {
	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, ".swarm-deploy")
	repoDir := filepath.Join(dataDir, "repo")
	configDir := filepath.Join(rootDir, "config")

	require.NoError(t, writeStacksFile(filepath.Join(configDir, "stacks.yaml"), "from-config"), "write config stacks")
	require.NoError(t, writeStacksFile(filepath.Join(repoDir, "stacks.yaml"), "from-repo"), "write repo stacks")

	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			StacksSource: config.StacksSourceSpec{
				File: "./stacks.yaml",
			},
		},
	}

	repository := git.NewRepository(config.GitSpec{}, filepath.Join(dataDir, "repo"))

	c := &Controller{
		cfg:    cfg,
		git:    repository,
		tracer: otel.Tracer("test"),
	}

	loadedFrom, err := c.reloadStacks()
	require.NoError(t, err, "reload stacks")
	assert.Equal(t, filepath.Join(repoDir, "stacks.yaml"), loadedFrom, "expected path from repo")
	require.Len(t, c.cfg.Spec.Stacks, 1, "expected one stack")
	assert.Equal(t, "from-repo", c.cfg.Spec.Stacks[0].Name, "expected stack loaded from repo")
}

func TestReloadNetworksUsesRepositoryDirFirst(t *testing.T) {
	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, ".swarm-deploy")
	repoDir := filepath.Join(dataDir, "repo")
	configDir := filepath.Join(rootDir, "config")

	require.NoError(
		t,
		writeNetworksFile(filepath.Join(configDir, "networks.yaml"), "from-config"),
		"write config networks",
	)
	require.NoError(
		t,
		writeNetworksFile(filepath.Join(repoDir, "networks.yaml"), "from-repo"),
		"write repo networks",
	)

	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			NetworksSource: config.NetworksSourceSpec{
				File: "./networks.yaml",
			},
		},
	}

	repository := git.NewRepository(config.GitSpec{}, filepath.Join(dataDir, "repo"))

	c := &Controller{
		cfg:    cfg,
		git:    repository,
		tracer: otel.Tracer("test"),
	}

	loadedFrom, err := c.reloadNetworks()
	require.NoError(t, err, "reload networks")
	assert.Equal(t, filepath.Join(repoDir, "networks.yaml"), loadedFrom, "expected path from repo")
	require.Len(t, c.cfg.Spec.Networks, 1, "expected one network")
	assert.Equal(t, "from-repo", c.cfg.Spec.Networks[0].Name, "expected network loaded from repo")
}

func TestControllerSyncOnceReconcilesStacksWhenGitRevisionUnchanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), ".swarm-deploy")

	require.NoError(
		t,
		writeStacksConfigFile(filepath.Join(repoDir, "stacks.yaml"), []stackFileSpec{
			{Name: "app", ComposeFile: "app.yaml"},
		}),
		"write stacks config",
	)
	require.NoError(
		t,
		writeComposeFile(filepath.Join(repoDir, "app.yaml"), "api"),
		"write compose file",
	)

	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{
		OldRevision: "commit-1",
		NewRevision: "commit-1",
		Updated:     false,
	}, nil)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	gomock.InOrder(
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil),
	)

	store := modelstore.NewMemoryStore()
	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			Git: config.GitSpec{
				Repository: "repo",
			},
			StacksSource: config.StacksSourceSpec{
				File: "stacks.yaml",
			},
		},
	}
	eventDispatcher := &dispatcher.NopDispatcher{}
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_sync_once_unchanged",
	})
	controller := &Controller{
		cfg:               cfg,
		git:               repository,
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: newNetworkReconciler(nil),
		stackReconciler: stackloop.New(
			cfg,
			repository,
			stackDeployer,
			&swarm.Swarm{
				Services: serviceManager,
			},
			eventDispatcher,
			metricGroup.Deploys,
			store,
			fs.NopFileSystem{},
		),
		tracer: otel.Tracer("test"),
	}

	controller.syncOnce(context.Background(), triggerTask{
		reason: TriggerPoll,
	})

	state := store.Get()
	stackState, exists := state.Stack("app")
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-1", stackState.LastCommit, "unexpected stack commit")
	assert.Equal(t, syncRunResultSuccess, state.LastSyncResult, "unexpected sync result")
	assert.Equal(t, "commit-1", state.GitRevision, "unexpected git revision")
}

func TestControllerSyncOncePrioritizesChangedStacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), ".swarm-deploy")

	require.NoError(
		t,
		writeStacksConfigFile(filepath.Join(repoDir, "stacks.yaml"), []stackFileSpec{
			{Name: "stack-a", ComposeFile: "stack-a.yaml"},
			{Name: "stack-b", ComposeFile: "stack-b.yaml"},
		}),
		"write stacks config",
	)
	require.NoError(t, writeComposeFile(filepath.Join(repoDir, "stack-a.yaml"), "api"), "write compose a")
	require.NoError(t, writeComposeFile(filepath.Join(repoDir, "stack-b.yaml"), "worker"), "write compose b")

	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{
		OldRevision: "commit-1",
		NewRevision: "commit-2",
		Updated:     true,
	}, nil)
	repository.EXPECT().Diff(gomock.Any(), "commit-1", "commit-2").Return([]git.CommitFileDiff{
		{NewPath: "stack-b.yaml"},
	}, nil)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	gomock.InOrder(
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-b", gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-b").Return(nil, nil),
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-a", gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-a").Return(nil, nil),
	)

	store := modelstore.NewMemoryStore()
	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			Git: config.GitSpec{
				Repository: "repo",
			},
			StacksSource: config.StacksSourceSpec{
				File: "stacks.yaml",
			},
		},
	}
	eventDispatcher := &dispatcher.NopDispatcher{}
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_sync_once_order",
	})
	controller := &Controller{
		cfg:               cfg,
		git:               repository,
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: newNetworkReconciler(nil),
		stackReconciler: stackloop.New(
			cfg,
			repository,
			stackDeployer,
			&swarm.Swarm{
				Services: serviceManager,
			},
			eventDispatcher,
			metricGroup.Deploys,
			store,
			fs.NopFileSystem{},
		),
		tracer: otel.Tracer("test"),
	}

	controller.syncOnce(context.Background(), triggerTask{
		reason: TriggerPoll,
	})

	state := store.Get()
	assert.Equal(t, syncRunResultSuccess, state.LastSyncResult, "unexpected sync result")
	assert.Equal(t, "commit-2", state.GitRevision, "unexpected git revision")
}

func TestControllerSyncOnceContinuesWhenGitDiffFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), ".swarm-deploy")

	require.NoError(
		t,
		writeStacksConfigFile(filepath.Join(repoDir, "stacks.yaml"), []stackFileSpec{
			{Name: "stack-a", ComposeFile: "stack-a.yaml"},
			{Name: "stack-b", ComposeFile: "stack-b.yaml"},
		}),
		"write stacks config",
	)
	require.NoError(t, writeComposeFile(filepath.Join(repoDir, "stack-a.yaml"), "api"), "write compose a")
	require.NoError(t, writeComposeFile(filepath.Join(repoDir, "stack-b.yaml"), "worker"), "write compose b")

	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{
		OldRevision: "commit-1",
		NewRevision: "commit-2",
		Updated:     true,
	}, nil)
	repository.EXPECT().Diff(gomock.Any(), "commit-1", "commit-2").Return(nil, errors.New("diff failed"))
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	gomock.InOrder(
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-a", gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-a").Return(nil, nil),
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-b", gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-b").Return(nil, nil),
	)

	store := modelstore.NewMemoryStore()
	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			Git: config.GitSpec{
				Repository: "repo",
			},
			StacksSource: config.StacksSourceSpec{
				File: "stacks.yaml",
			},
		},
	}
	eventDispatcher := &dispatcher.NopDispatcher{}
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_sync_once_diff_error",
	})
	controller := &Controller{
		cfg:               cfg,
		git:               repository,
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: newNetworkReconciler(nil),
		stackReconciler: stackloop.New(
			cfg,
			repository,
			stackDeployer,
			&swarm.Swarm{
				Services: serviceManager,
			},
			eventDispatcher,
			metricGroup.Deploys,
			store,
			fs.NopFileSystem{},
		),
		tracer: otel.Tracer("test"),
	}

	controller.syncOnce(context.Background(), triggerTask{
		reason: TriggerPoll,
	})

	state := store.Get()
	assert.Equal(t, syncRunResultSuccess, state.LastSyncResult, "unexpected sync result")
	assert.Equal(t, "commit-2", state.GitRevision, "unexpected git revision")
}

func writeStacksFile(path string, stackName string) error {
	return writeStacksConfigFile(path, []stackFileSpec{
		{Name: stackName, ComposeFile: "app.yaml"},
	})
}

type stackFileSpec struct {
	Name        string
	ComposeFile string
}

func writeStacksConfigFile(path string, stacks []stackFileSpec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "stacks:\n"
	for _, stack := range stacks {
		content += "  - name: " + stack.Name + "\n"
		content += "    composeFile: " + stack.ComposeFile + "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeComposeFile(path string, serviceName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := []byte("services:\n  " + serviceName + ":\n    image: nginx:1.0\n")
	return os.WriteFile(path, content, 0o600)
}

func writeNetworksFile(path string, networkName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := []byte("networks:\n  - name: " + networkName + "\n")
	return os.WriteFile(path, content, 0o600)
}
