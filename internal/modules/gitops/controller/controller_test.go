package controller

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
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackplan"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	git "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.uber.org/mock/gomock"
)

func TestControllerWebhookEmitsReceivedEvent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		shutdown   bool
		queueFull  bool
		wantQueued bool
	}{
		{name: "queued", wantQueued: true},
		{name: "queue full", queueFull: true},
		{name: "shutting down", shutdown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
			eventDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).Do(
				func(_ context.Context, event events.Event) {
					received, ok := event.(*events.WebhookReceived)
					require.True(t, ok, "expected WebhookReceived")
					assert.Equal(t, tc.wantQueued, received.Queued)
				},
			).Times(1)

			controller := &Controller{
				event:       eventDispatcher,
				reconcileCh: make(chan reconcileTask, 1),
			}
			if tc.queueFull {
				controller.reconcileCh <- reconcileTask{reason: TriggerInterval}
			}
			if tc.shutdown {
				controller.shuttingDown.Store(true)
			}

			queued, err := controller.Webhook(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tc.wantQueued, queued)
			if tc.wantQueued {
				assert.Equal(t, TriggerWebhook, (<-controller.reconcileCh).reason)
			}
		})
	}
}

func TestControllerCleanupStacksUsesDedicatedCleanupPath(t *testing.T) {
	reconciler := &recordingStackReconciler{
		cleanupErrByStack: map[string]error{
			"broken": errors.New("cleanup failed"),
		},
	}
	cfg := &config.Config{Spec: config.Spec{StacksSource: config.StacksSourceSpec{File: "stacks.yaml"}}}
	dir := t.TempDir()
	require.NoError(t, writeStacksConfigFile(filepath.Join(dir, "stacks.yaml"), []stackFileSpec{
		{Name: "app", ComposeFile: "app.yaml"},
		{Name: "broken", ComposeFile: "broken.yaml"},
		{Name: "worker", ComposeFile: "worker.yaml"},
	}))
	_, err := cfg.Reload(dir)
	require.NoError(t, err)
	controller := &Controller{
		configStore:     configstore.New(cfg, &dispatcher.NopDispatcher{}),
		stackReconciler: reconciler,
	}

	controller.cleanupStacks(context.Background())

	assert.Equal(t, []string{"app", "broken", "worker"}, reconciler.cleanedStacks)
	assert.Zero(t, reconciler.reconcileCalls, "periodic cleanup must not run full reconciliation")
}

func TestSyncStacksContinuesAfterOneStackRecoveryFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	broken := config.StackSpec{Name: "broken"}
	healthy := config.StackSpec{Name: "healthy"}
	recoveryErr := errors.New("recover deployment result: transient sqlite failure")
	reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
		Stack: broken, Commit: "revision",
	}).Return(recoveryErr)
	reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
		Stack: healthy, Commit: "revision",
	}).Return(nil)
	controller := &Controller{stackReconciler: reconciler}

	errs := controller.syncStacks(t.Context(), []config.StackSpec{broken, healthy}, "revision", TriggerInterval)

	require.Len(t, errs, 1)
	assert.ErrorIs(t, errs[0], recoveryErr)
}

type recordingStackReconciler struct {
	cleanedStacks     []string
	cleanupErrByStack map[string]error
	reconcileCalls    int
}

func (r *recordingStackReconciler) Reconcile(context.Context, stackloop.ReconciliationRequest) error {
	r.reconcileCalls++
	return nil
}

func (r *recordingStackReconciler) Cleanup(_ context.Context, stack config.StackSpec) error {
	r.cleanedStacks = append(r.cleanedStacks, stack.Name)
	return r.cleanupErrByStack[stack.Name]
}

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

			eventDispatcher := &dispatcher.NopDispatcher{}
			db, store := newSQLState(t)

			metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
				Namespace: "test_controller_graceful_shutdown",
			})
			controller := &Controller{
				db: db,
				cfg: &config.Config{Spec: config.Spec{
					Git: config.GitSpec{Repository: "repo"},
					Sync: config.SyncSpec{
						Mode:         testCase.mode,
						PollInterval: specw.Duration{Value: 5 * time.Millisecond},
						Interval:     specw.Duration{Value: time.Hour},
					},
				}},
				git:          repository,
				stackPlanner: stackplan.NewPlanner(repository),
				metrics:      metricGroup,
				event:        eventDispatcher,
				stateStore:   store,
				reconcileCh:  make(chan reconcileTask, 1),
				tracer:       otel.Tracer("test"),
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

func TestControllersSharingSQLiteDoNotReconcileConcurrently(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	staleRenderedPath := filepath.Join(dataDir, "rendered", "removed-stack.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(staleRenderedPath), 0o755), "create rendered dir")
	require.NoError(t, os.WriteFile(staleRenderedPath, []byte("sensitive"), 0o600), "write stale rendered compose")
	db, err := storage.Open(ctx, dataDir)
	require.NoError(t, err)
	defer db.Close()
	ctrl := gomock.NewController(t)
	firstRepository := git.NewMockRepository(ctrl)
	secondRepository := git.NewMockRepository(ctrl)
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstRepository.EXPECT().Pull(gomock.Any()).DoAndReturn(func(context.Context) (git.PullResult, error) {
		close(firstStarted)
		<-releaseFirst
		return git.PullResult{}, errors.New("first stopped")
	})
	secondRepository.EXPECT().Pull(gomock.Any()).DoAndReturn(func(context.Context) (git.PullResult, error) {
		close(secondStarted)
		return git.PullResult{}, errors.New("second stopped")
	})
	events := &dispatcher.NopDispatcher{}
	newController := func(repository git.Repository) *Controller {
		return &Controller{
			db: db,
			cfg: &config.Config{Spec: config.Spec{
				DataDir: dataDir,
				Sync:    config.SyncSpec{Interval: specw.Duration{Value: time.Hour}},
			}},
			git:          repository,
			stackPlanner: stackplan.NewPlanner(repository),
			metrics:      metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_controller_ownership"}),
			event:        events,
			stateStore:   modelstore.NewSQLStore(db),
			reconcileCh:  make(chan reconcileTask, 1),
			ownership:    newControllerOwnership(dataDir),
			recovery:     deployment.NewService(db, events),
			shutdownWait: 100 * time.Millisecond,
			tracer:       otel.Tracer("test"),
		}
	}
	first, second := newController(firstRepository), newController(secondRepository)
	first.ownership.retryInterval = time.Millisecond
	second.ownership.retryInterval = time.Millisecond
	firstCtx, cancelFirst := context.WithCancel(ctx)
	secondCtx, cancelSecond := context.WithCancel(ctx)
	defer cancelFirst()
	defer cancelSecond()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- first.Run(firstCtx) }()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "first controller did not start reconciliation")
	}
	assert.NoFileExists(t, staleRenderedPath, "stale rendered compose must be removed before reconciliation")
	go func() { secondDone <- second.Run(secondCtx) }()
	select {
	case <-secondStarted:
		require.FailNow(t, "second controller reconciled while first held ownership")
	case <-time.After(50 * time.Millisecond):
	}

	cancelFirst()
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		require.FailNow(t, "second controller did not reconcile after clean handoff")
	}
	cancelSecond()
	require.NoError(t, <-secondDone)
}

func TestNewControllerDoesNotRemoveRenderedComposeOwnedByActiveController(t *testing.T) {
	dataDir := t.TempDir()
	owner := newControllerOwnership(dataDir)
	handle, err := owner.acquire(t.Context())
	require.NoError(t, err, "acquire first controller ownership")
	t.Cleanup(func() { require.NoError(t, handle.release(), "release first controller ownership") })

	renderedPath := filepath.Join(dataDir, "rendered", "app.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(renderedPath), 0o755), "create rendered dir")
	require.NoError(t, os.WriteFile(renderedPath, []byte("active deployment"), 0o600),
		"write active controller rendered compose")

	db, stateStore := newSQLState(t)
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_controller_start_first"})
	_ = New(
		&config.Config{Spec: config.Spec{DataDir: dataDir}},
		nil,
		nil,
		&swarm.Swarm{},
		nil,
		metricGroup,
		&dispatcher.NopDispatcher{},
		stateStore,
		fs.NewLocalFileSystem(),
		db,
	)

	assert.FileExists(t, renderedPath,
		"constructing a waiting controller must not remove the active controller rendered compose")
}

func TestControllerCancelsReconciliationAfterShutdownGracePeriod(t *testing.T) {
	ctrl := gomock.NewController(t)
	started := make(chan struct{})
	canceled := make(chan error, 1)
	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).DoAndReturn(func(ctx context.Context) (git.PullResult, error) {
		close(started)
		<-ctx.Done()
		canceled <- ctx.Err()
		return git.PullResult{}, ctx.Err()
	})
	db, store := newSQLState(t)
	controller := &Controller{
		db: db,
		cfg: &config.Config{Spec: config.Spec{
			Git:  config.GitSpec{Repository: "repo"},
			Sync: config.SyncSpec{Interval: specw.Duration{Value: time.Hour}},
		}},
		git:          repository,
		stackPlanner: stackplan.NewPlanner(repository),
		metrics:      metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_controller_shutdown_timeout"}),
		event:        &dispatcher.NopDispatcher{},
		stateStore:   store,
		reconcileCh:  make(chan reconcileTask, 1),
		shutdownWait: 20 * time.Millisecond,
		tracer:       otel.Tracer("test"),
	}
	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- controller.Run(runCtx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		require.FailNow(t, "reconciliation did not start")
	}
	cancel()
	select {
	case <-canceled:
		require.FailNow(t, "reconciliation was canceled before its grace period")
	case <-time.After(5 * time.Millisecond):
	}
	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.FailNow(t, "reconciliation context was not canceled after its grace period")
	}
	require.NoError(t, <-done)
}

func TestConfigStoreReloadUsesRepositoryDirFirst(t *testing.T) {
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
		cfg:          cfg,
		configStore:  configstore.New(cfg, &dispatcher.NopDispatcher{}),
		git:          repository,
		stackPlanner: stackplan.NewPlanner(repository),
		tracer:       otel.Tracer("test"),
	}

	err := c.configStore.Reload(t.Context(), c.git.WorkingDir())
	require.NoError(t, err, "reload stacks")
	stacks := c.configStore.Stacks()
	require.Len(t, stacks, 1, "expected one stack")
	assert.Equal(t, "from-repo", stacks[0].Name, "expected stack loaded from repo")
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
	require.NoError(t, writeStacksFile(filepath.Join(configDir, "stacks.yaml"), "from-config"))
	require.NoError(t, writeStacksFile(filepath.Join(repoDir, "stacks.yaml"), "from-repo"))

	cfg := &config.Config{
		Spec: config.Spec{
			DataDir:      dataDir,
			StacksSource: config.StacksSourceSpec{File: "./stacks.yaml"},
			NetworksSource: config.NetworksSourceSpec{
				File: "./networks.yaml",
			},
		},
	}

	repository := git.NewRepository(config.GitSpec{}, filepath.Join(dataDir, "repo"))

	c := &Controller{
		cfg:          cfg,
		configStore:  configstore.New(cfg, &dispatcher.NopDispatcher{}),
		git:          repository,
		stackPlanner: stackplan.NewPlanner(repository),
		tracer:       otel.Tracer("test"),
	}

	err := c.configStore.Reload(t.Context(), c.git.WorkingDir())
	require.NoError(t, err, "reload networks")
	networks := c.configStore.Networks()
	require.Len(t, networks, 1, "expected one network")
	assert.Equal(t, "from-repo", networks[0].Name, "expected network loaded from repo")
}

func TestControllerSyncContinuesWhenConfigurationEventPublicationFails(t *testing.T) {
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
	repository.EXPECT().Head(gomock.Any()).Return("commit-1", nil)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()

	serviceManager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	gomock.InOrder(
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return(nil, nil),
	)

	db, store := newSQLState(t)
	_ = db
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
	eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
	eventDispatcher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event events.Event) error {
			if _, configurationChanged := event.(*events.ConfigurationChanged); configurationChanged {
				return errors.New("outbox unavailable")
			}
			return nil
		},
	).AnyTimes()
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_sync_once_unchanged",
	})
	controller := &Controller{db: db,
		cfg:               cfg,
		configStore:       configstore.New(cfg, eventDispatcher),
		git:               repository,
		stackPlanner:      stackplan.NewPlanner(repository),
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: networkloop.New(nil),
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
			fs.NopFileSystem{}, db,
		),
		tracer: otel.Tracer("test"),
	}

	controller.reconcile(context.Background(), reconcileTask{
		reason: TriggerInterval,
	}, nil)

	state := readSQLState(t, store)
	stackState, exists := state.Stack("app")
	require.True(t, exists, "expected stack state")
	assert.Equal(t, "commit-1", stackState.LastCommit, "unexpected stack commit")
	assert.Equal(t, syncRunResultSuccess, state.LastSyncResult, "unexpected sync result")
	assert.Equal(t, "commit-1", state.GitRevision, "unexpected git revision")
}

func TestControllerPollGitSkipsReconcileWhenGitHasNoChanges(t *testing.T) {
	ctrl := gomock.NewController(t)

	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{
		OldRevision: "commit-1",
		NewRevision: "commit-1",
		Updated:     false,
	}, nil)

	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_sync_once_poll_no_change",
	})
	db, store := newSQLState(t)
	controller := &Controller{
		db: db,
		cfg: &config.Config{Spec: config.Spec{
			Git: config.GitSpec{Repository: "repo"},
		}},
		git:          repository,
		stackPlanner: stackplan.NewPlanner(repository),
		metrics:      metricGroup,
		event:        &dispatcher.NopDispatcher{},
		stateStore:   store,
		tracer:       otel.Tracer("test"),
	}

	controller.pollGit(context.Background())

	state, err := controller.stateStore.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, syncRunResultNoChange, state.LastPollResult, "unexpected poll result")
	assert.Empty(t, state.LastPollError, "unexpected poll error")
}

func TestControllerPollGitRecordsErrorState(t *testing.T) {
	ctrl := gomock.NewController(t)

	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{}, errors.New("git unavailable"))

	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{
		Namespace: "test_poll_error",
	})
	db, store := newSQLState(t)
	_ = db
	controller := &Controller{db: db,
		cfg: &config.Config{Spec: config.Spec{
			Git: config.GitSpec{Repository: "repo"},
		}},
		git:          repository,
		stackPlanner: stackplan.NewPlanner(repository),
		metrics:      metricGroup,
		event:        &dispatcher.NopDispatcher{},
		stateStore:   store,
		tracer:       otel.Tracer("test"),
	}

	controller.pollGit(context.Background())

	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastPollResult, "unexpected poll result")
	assert.Equal(t, "Git pull failed", state.LastPollError, "unexpected poll error")
	assert.Empty(t, state.LastSyncResult, "poll error must not overwrite sync state")
}

func TestGitPullRecoveryStateSurvivesControllerRestart(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullSucceeded{})).Return(nil)

	first := &Controller{db: db, stateStore: store, event: publisher}
	require.NoError(t, first.recordGitPullFailure(t.Context(), "git.example.com/team/app",
		git.FailureAuthentication, "Git repository authentication failed", TriggerPoll))
	state := readSQLState(t, store)
	require.NotNil(t, state.GitPullFailure)

	second := &Controller{db: db, stateStore: modelstore.NewSQLStore(db), event: publisher}
	require.NoError(t, second.recordGitPullSuccess(t.Context(), "git.example.com/team/app", TriggerStartup))
	state = readSQLState(t, store)
	assert.Nil(t, state.GitPullFailure)
}

func TestSuccessfulPullFromChangedRepositoryRemovesPreviousFailureWithoutRecovery(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{NewRevision: "new"}, nil)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ConfigurationChanged{})).DoAndReturn(
			func(_ context.Context, event events.Event) error {
				changed := event.(*events.ConfigurationChanged)
				assert.Equal(t, "git.example.com/team/old", changed.PreviousRepository)
				assert.Equal(t, "git.example.com/team/new", changed.Repository)
				return nil
			},
		),
	)
	controller := &Controller{
		db: db, stateStore: store, git: repository, event: publisher,
		cfg:     &config.Config{Spec: config.Spec{Git: config.GitSpec{Repository: "https://git.example.com/team/new.git"}}},
		metrics: metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_changed_repository_success"}),
	}
	require.NoError(t, controller.recordGitPullFailure(t.Context(), "git.example.com/team/old",
		git.FailureAuthentication, "Git repository authentication failed", TriggerPoll))

	_, err := controller.pullGit(t.Context(), TriggerStartup)
	require.NoError(t, err)
	assert.Nil(t, readSQLState(t, store).GitPullFailure)
}

func TestFailedPullFromChangedRepositoryRemovesPreviousAndRecordsCurrentFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{}, errors.New("unavailable"))
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ConfigurationChanged{})).DoAndReturn(
			func(_ context.Context, event events.Event) error {
				changed := event.(*events.ConfigurationChanged)
				assert.Equal(t, "git.example.com/team/old", changed.PreviousRepository)
				assert.Equal(t, "git.example.com/team/new", changed.Repository)
				return nil
			},
		),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
			func(_ context.Context, event events.Event) error {
				assert.Equal(t, "git.example.com/team/new", event.(*events.GitPullFailed).Repository)
				return nil
			},
		),
	)
	controller := &Controller{
		db: db, stateStore: store, git: repository, event: publisher,
		cfg:     &config.Config{Spec: config.Spec{Git: config.GitSpec{Repository: "https://git.example.com/team/new.git"}}},
		metrics: metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_changed_repository_failure"}),
	}
	require.NoError(t, controller.recordGitPullFailure(t.Context(), "git.example.com/team/old",
		git.FailureAuthentication, "Git repository authentication failed", TriggerPoll))

	_, err := controller.pullGit(t.Context(), TriggerStartup)
	require.Error(t, err)
	state := readSQLState(t, store)
	require.NotNil(t, state.GitPullFailure)
	assert.Equal(t, "git.example.com/team/new", state.GitPullFailure.Repository)
}

func TestRepositoryChangePublicationFailureRetainsPreviousFailureState(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ConfigurationChanged{})).
			Return(errors.New("outbox unavailable")),
	)
	controller := &Controller{db: db, stateStore: store, event: publisher}
	require.NoError(t, controller.recordGitPullFailure(t.Context(), "git.example.com/team/old",
		git.FailureAuthentication, "Git repository authentication failed", TriggerPoll))

	err := controller.recordGitPullSuccess(t.Context(), "git.example.com/team/new", TriggerStartup)
	require.ErrorContains(t, err, "outbox unavailable")
	state := readSQLState(t, store)
	require.NotNil(t, state.GitPullFailure)
	assert.Equal(t, "git.example.com/team/old", state.GitPullFailure.Repository)
}

func TestGitPullStateAndEventPublicationRollBackTogether(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).
		Return(errors.New("outbox unavailable"))
	controller := &Controller{db: db, stateStore: store, event: publisher}

	err := controller.recordGitPullFailure(t.Context(), "git.example.com/team/app",
		git.FailureAuthentication, "Git repository authentication failed", TriggerManual)
	require.ErrorContains(t, err, "outbox unavailable")
	state := readSQLState(t, store)
	assert.Nil(t, state.GitPullFailure, "failure state must roll back when its event cannot be published")
}

func TestGitPullRecoveryPublicationFailureRetainsFailedState(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullSucceeded{})).
			Return(errors.New("outbox unavailable")),
	)
	controller := &Controller{db: db, stateStore: store, event: publisher}
	require.NoError(t, controller.recordGitPullFailure(t.Context(), "git.example.com/team/app",
		git.FailureAuthentication, "Git repository authentication failed", TriggerPoll))

	err := controller.recordGitPullSuccess(t.Context(), "git.example.com/team/app", TriggerWebhook)
	require.ErrorContains(t, err, "outbox unavailable")
	state := readSQLState(t, store)
	require.NotNil(t, state.GitPullFailure, "recovery state must roll back when its event cannot be published")
}

func TestGitPullRecoveryPublicationFailureDoesNotChangeSuccessfulPull(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	repository := git.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{OldRevision: "one", NewRevision: "two", Updated: true}, nil)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).Return(nil),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullSucceeded{})).
			Return(errors.New("outbox unavailable")),
	)
	controller := &Controller{
		db: db, stateStore: store, git: repository, event: publisher,
		cfg:     &config.Config{Spec: config.Spec{Git: config.GitSpec{Repository: "https://git.example.com/team/app.git"}}},
		metrics: metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_pull_recovery_publication"}),
	}
	require.NoError(t, controller.recordGitPullFailure(t.Context(), "git.example.com/team/app",
		git.FailureNetwork, "Git repository is temporarily unreachable", TriggerPoll))

	result, err := controller.pullGit(t.Context(), TriggerPoll)
	require.NoError(t, err)
	assert.True(t, result.Updated)
	assert.Equal(t, "two", result.NewRevision)
	assert.NotNil(t, readSQLState(t, store).GitPullFailure,
		"failed recovery publication must remain retryable after a successful pull")
}

func TestNormalGitPullSuccessDoesNotPublishRecovery(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, store := newSQLState(t)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	controller := &Controller{db: db, stateStore: store, event: publisher}

	require.NoError(t, controller.recordGitPullSuccess(t.Context(), "git.example.com/team/app", TriggerPoll))
}

func TestGitPullFailureMonitoringCoversEveryPullTrigger(t *testing.T) {
	for _, reason := range []TriggerReason{TriggerStartup, TriggerPoll, TriggerManual, TriggerWebhook} {
		t.Run(string(reason), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			repository := git.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{},
				errors.New("https://robot:private-token@git.example.com failed"))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					failed := event.(*events.GitPullFailed)
					assert.Equal(t, string(reason), failed.TriggerReason)
					assert.Equal(t, "git.example.com/team/app", failed.Repository)
					assert.NotContains(t, failed.Description, "private-token")
					return nil
				},
			)
			controller := &Controller{
				db: db, cfg: &config.Config{Spec: config.Spec{Git: config.GitSpec{
					Repository: "https://robot:private-token@git.example.com/team/app.git",
				}}},
				git: repository, event: publisher, stateStore: store,
				metrics: metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test_git_pull_trigger_" + string(reason)}),
			}

			_, err := controller.pullGit(t.Context(), reason)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestGitPullFailureWithMalformedCredentialURLPublishesSafeIdentifier(t *testing.T) {
	const secret = "private-token"
	for index, remote := range []string{
		"https://robot:" + secret + "%zz@git.example.com/team/app.git",
		"https:robot:" + secret + "@git.example.com/team/app.git",
		"https:robot:" + secret + "%zz@git.example.com/team/app.git",
		"https:/",
	} {
		t.Run(remote, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			db, store := newSQLState(t)
			repository := git.NewMockRepository(ctrl)
			repository.EXPECT().Pull(gomock.Any()).Return(git.PullResult{}, errors.New("remote unavailable"))
			publisher := dispatcher.NewMockDispatcher(ctrl)
			publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.GitPullFailed{})).DoAndReturn(
				func(_ context.Context, event events.Event) error {
					failed := event.(*events.GitPullFailed)
					assert.Regexp(t, `^repository-[0-9a-f]{16}$`, failed.Repository)
					assert.NotContains(t, failed.Repository, secret)
					return nil
				},
			)
			controller := &Controller{
				db: db, stateStore: store, git: repository, event: publisher,
				cfg: &config.Config{Spec: config.Spec{Git: config.GitSpec{Repository: remote}}},
				metrics: metrics.NewGroup(metrics.CreateGroupParams{
					Namespace: fmt.Sprintf("test_git_pull_malformed_remote_%d", index),
				}),
			}

			_, err := controller.pullGit(t.Context(), TriggerStartup)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), secret)
			state := readSQLState(t, store)
			require.NotNil(t, state.GitPullFailure)
			assert.Regexp(t, `^repository-[0-9a-f]{16}$`, state.GitPullFailure.Repository)
			assert.NotContains(t, state.GitPullFailure.Repository, secret)
		})
	}
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
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-b", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-b").Return(nil, nil),
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-a", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-a").Return(nil, nil),
	)

	db, store := newSQLState(t)
	_ = db
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
	controller := &Controller{db: db,
		cfg:               cfg,
		configStore:       configstore.New(cfg, eventDispatcher),
		git:               repository,
		stackPlanner:      stackplan.NewPlanner(repository),
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: networkloop.New(nil),
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
			fs.NopFileSystem{}, db,
		),
		tracer: otel.Tracer("test"),
	}

	controller.pollGit(context.Background())

	state := readSQLState(t, store)
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
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-a", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-a").Return(nil, nil),
		stackDeployer.EXPECT().DeployStack(gomock.Any(), "stack-b", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		serviceManager.EXPECT().ListStackServices(gomock.Any(), "stack-b").Return(nil, nil),
	)

	db, store := newSQLState(t)
	_ = db
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
	controller := &Controller{db: db,
		cfg:               cfg,
		configStore:       configstore.New(cfg, eventDispatcher),
		git:               repository,
		stackPlanner:      stackplan.NewPlanner(repository),
		metrics:           metricGroup,
		event:             eventDispatcher,
		stateStore:        store,
		networkReconciler: networkloop.New(nil),
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
			fs.NopFileSystem{}, db,
		),
		tracer: otel.Tracer("test"),
	}

	controller.pollGit(context.Background())

	state := readSQLState(t, store)
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
