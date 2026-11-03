package syncrun

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"go.uber.org/mock/gomock"
)

const testWait = 5 * time.Second

func TestRunDispatchesStacksInPlanOrderAndContinuesAfterFailure(t *testing.T) {
	tests := []struct {
		name     string
		diff     []gitx.CommitFileDiff
		diffErr  error
		expected []string
	}{
		{name: "changed stacks first", diff: []gitx.CommitFileDiff{{NewPath: "stack-b.yaml"}}, expected: []string{"stack-b", "stack-a"}},
		{name: "diff failure keeps configured order", diffErr: errors.New("diff failed"), expected: []string{"stack-a", "stack-b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repoDir := t.TempDir()
			require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"stack-a", "stack-b"}))
			repository := gitx.NewMockRepository(ctrl)
			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			repository.EXPECT().Diff(gomock.Any(), "old", "new").Return(tt.diff, tt.diffErr)
			reconciler := stackloop.NewMockStackReconciler(ctrl)
			sources := expectLoads(reconciler, "stack-a", "stack-b")
			calls := make([]any, 0, len(tt.expected))
			for index, name := range tt.expected {
				var reconcileErr error
				if index == 0 {
					reconcileErr = errors.New("deploy failed")
				}
				calls = append(calls, reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
					Stack: config.StackSpec{Name: name, ComposeFile: name + ".yaml"}, Commit: "new", Source: sources[name],
				}).Return(reconcileErr))
			}
			gomock.InOrder(calls...)
			runner, store, queue := newTestRunner(t, repository, reconciler, 1)

			runner.run(t.Context(), Task{Reason: TriggerPoll}, &gitx.PullResult{
				OldRevision: "old", NewRevision: "new", Updated: true,
			})

			waitIdle(t, queue)
			state := readSQLState(t, store)
			assert.Equal(t, syncRunResultSuccess, state.LastSyncResult, "stack failures are tracked per stack")
			assert.Empty(t, state.LastSyncError)
			assert.Equal(t, "new", state.GitRevision)
		})
	}
}

func TestRunDoesNotWaitForSlowStack(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"slow", "fast"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	reconciler.EXPECT().Load(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, stack config.StackSpec) (*compose.File, error) {
			return &compose.File{Path: stack.ComposeFile}, nil
		}).AnyTimes()

	slowStarted, releaseSlow := make(chan struct{}), make(chan struct{})
	fastCommits := make(chan string, 2)
	var slowCommits []string
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			if req.Stack.Name == "fast" {
				fastCommits <- req.Commit
				return nil
			}
			slowCommits = append(slowCommits, req.Commit)
			if len(slowCommits) == 1 {
				close(slowStarted)
				<-releaseSlow
			}
			return nil
		}).Times(5)
	runner, store, queue := newTestRunner(t, repository, reconciler, 2)

	for _, revision := range []string{"r1", "r2", "r3"} {
		runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: revision, NewRevision: revision})
		if revision == "r1" {
			receive(t, slowStarted)
		}
		if revision != "r3" {
			assert.Equal(t, revision, receive(t, fastCommits), "the fast stack must not wait for the slow one")
		}
	}
	assert.Equal(t, "r3", readSQLState(t, store).GitRevision, "runs must complete while a stack is still deploying")

	close(releaseSlow)
	waitIdle(t, queue)
	assert.Equal(t, "r3", receive(t, fastCommits))
	assert.Equal(t, []string{"r1", "r3"}, slowCommits, "requests queued behind a busy stack are coalesced to the latest")
}

func TestRunDropsQueuedWorkOfStacksRemovedFromConfiguration(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	stacksPath := filepath.Join(repoDir, "stacks.yaml")
	require.NoError(t, writeStacks(stacksPath, []string{"slow", "old"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	reconciler.EXPECT().Load(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, stack config.StackSpec) (*compose.File, error) {
			return &compose.File{Path: stack.ComposeFile}, nil
		}).AnyTimes()
	slowStarted, releaseSlow := make(chan struct{}), make(chan struct{})
	var reconciled []string
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			reconciled = append(reconciled, req.Stack.Name+"@"+req.Commit)
			if len(reconciled) == 1 {
				close(slowStarted)
				<-releaseSlow
			}
			return nil
		}).Times(2)
	runner, _, queue := newTestRunner(t, repository, reconciler, 1)

	// Revision A: the only worker is busy with slow, so old waits in the queue.
	runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "A", NewRevision: "A"})
	receive(t, slowStarted)
	require.True(t, queue.Busy("old"))

	// Revision B removes old from the configuration.
	require.NoError(t, writeStacks(stacksPath, []string{"slow"}))
	runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "B", NewRevision: "B"})
	assert.False(t, queue.Busy("old"), "work of a removed stack must not survive the configuration update")

	close(releaseSlow)
	waitIdle(t, queue)
	assert.Equal(t, []string{"slow@A", "slow@B"}, reconciled, "old must never be deployed from revision A")
}

func TestRunPassesLoadFailureToReconciliation(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"broken"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	loadErr := errors.New("invalid compose")
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	stack := config.StackSpec{Name: "broken", ComposeFile: "broken.yaml"}
	reconciler.EXPECT().Load(gomock.Any(), stack).Return(nil, loadErr)
	reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
		Stack: stack, Commit: "revision", IsManual: true, SourceErr: loadErr,
	}).Return(loadErr)
	runner, _, queue := newTestRunner(t, repository, reconciler, 1)

	runner.run(t.Context(), Task{Reason: TriggerManual, TriggeredBy: "alice"}, &gitx.PullResult{NewRevision: "revision"})

	waitIdle(t, queue)
}

func TestCleanupSkipsBusyStacks(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"busy", "idle"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	expectLoads(reconciler, "busy", "idle")
	busyStarted, releaseBusy := make(chan struct{}), make(chan struct{})
	reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req stackloop.ReconciliationRequest) error {
			if req.Stack.Name == "busy" {
				close(busyStarted)
				<-releaseBusy
			}
			return nil
		}).Times(2)
	runner, _, queue := newTestRunner(t, repository, reconciler, 2)
	runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{NewRevision: "revision"})
	receive(t, busyStarted)
	require.Eventually(t, func() bool { return !queue.Busy("idle") }, testWait, 10*time.Millisecond)

	// The busy stack is neither loaded nor cleaned up; the idle one is.
	idleSource := &compose.File{Path: "idle.yaml"}
	reconciler.EXPECT().Load(gomock.Any(), config.StackSpec{Name: "idle", ComposeFile: "idle.yaml"}).Return(idleSource, nil)
	reconciler.EXPECT().Cleanup(gomock.Any(), config.StackSpec{Name: "idle", ComposeFile: "idle.yaml"}, idleSource).Return(nil)

	runner.Cleanup(t.Context())

	close(releaseBusy)
	waitIdle(t, queue)
}

func TestPollFailureUpdatesOnlyPollState(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{}, errors.New("git unavailable"))
	runner, store, _ := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), 1)
	runner.Poll(t.Context())
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastPollResult)
	assert.Equal(t, "Git pull failed", state.LastPollError)
	assert.Empty(t, state.LastSyncResult)
}

func TestPollWithoutChangesDoesNotRunSynchronization(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{
		OldRevision: "same", NewRevision: "same",
	}, nil)
	runner, store, _ := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), 1)
	runner.Poll(t.Context())
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultNoChange, state.LastPollResult)
	assert.Empty(t, state.LastSyncResult)
}

func TestRunPullFailureUpdatesSyncState(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{}, errors.New("git unavailable"))
	runner, store, _ := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), 1)
	runner.Run(t.Context(), Task{Reason: TriggerStartup})
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastSyncResult)
	assert.Equal(t, string(TriggerStartup), state.LastSyncReason)
	assert.Equal(t, "Git pull failed", state.LastSyncError)
}

func TestRunConfigurationReloadFailurePersistsRevision(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(t.TempDir()).AnyTimes()
	runner, store, _ := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), 1)
	runner.run(t.Context(), Task{Reason: TriggerWebhook}, &gitx.PullResult{NewRevision: "revision"})
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastSyncResult)
	assert.Equal(t, "revision", state.GitRevision)
	assert.NotEmpty(t, state.LastSyncError)
}

func newTestRunner(
	t *testing.T, repository gitx.Repository, reconciler stackloop.StackReconciler, parallelism int,
) (*Runner, *modelstore.SQLStore, *stackqueue.Queue) {
	t.Helper()
	db, store := newSQLState(t)
	cfg := &config.Config{Spec: config.Spec{
		Git:          config.GitSpec{Repository: "repo"},
		StacksSource: config.StacksSourceSpec{File: "stacks.yaml"},
	}}
	events := &dispatcher.NopDispatcher{}
	namespace := strings.NewReplacer("/", "_", " ", "_").Replace("test_" + t.Name())
	metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: namespace})
	puller := gitpull.NewPuller(repository, gitpull.NewMonitor(db, store, events), metricGroup.Git, cfg.Spec.Git)
	configuration := configstore.New(cfg, events)
	networkCoordinator := networkloop.NewCoordinator(db, configuration, store, networkloop.New(nil), events)
	queue := stackqueue.New(reconciler, metricGroup.Sync, parallelism)
	queue.Start(context.WithoutCancel(t.Context()))
	t.Cleanup(queue.Stop)
	return New(configuration, repository, metricGroup, events, store,
		networkCoordinator, reconciler, puller, queue), store, queue
}

func expectLoads(reconciler *stackloop.MockStackReconciler, names ...string) map[string]*compose.File {
	sources := make(map[string]*compose.File, len(names))
	for _, name := range names {
		source := &compose.File{Path: name + ".yaml", Digest: name}
		sources[name] = source
		reconciler.EXPECT().Load(gomock.Any(), config.StackSpec{Name: name, ComposeFile: name + ".yaml"}).Return(source, nil)
	}
	return sources
}

func waitIdle(t *testing.T, queue *stackqueue.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), testWait)
	defer cancel()
	require.NoError(t, queue.WaitIdle(ctx), "stack queue did not become idle")
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(testWait):
		require.FailNow(t, "timed out waiting for stack operation")
		var zero T
		return zero
	}
}
