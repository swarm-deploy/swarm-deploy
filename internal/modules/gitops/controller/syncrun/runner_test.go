package syncrun

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"go.uber.org/mock/gomock"
)

func TestRunPrioritizesChangedStacksAndContinuesAfterFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"stack-a", "stack-b"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	repository.EXPECT().Diff(gomock.Any(), "old", "new").Return([]gitx.CommitFileDiff{{NewPath: "stack-b.yaml"}}, nil)
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	gomock.InOrder(
		reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
			Stack: config.StackSpec{Name: "stack-b", ComposeFile: "stack-b.yaml"}, Commit: "new",
		}).Return(errors.New("deploy failed")),
		reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
			Stack: config.StackSpec{Name: "stack-a", ComposeFile: "stack-a.yaml"}, Commit: "new",
		}).Return(nil),
	)
	runner, store := newTestRunner(t, repository, reconciler, repoDir)
	runner.run(t.Context(), Task{Reason: TriggerPoll}, &gitx.PullResult{
		OldRevision: "old", NewRevision: "new", Updated: true,
	})
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultPartialError, state.LastSyncResult)
	assert.Equal(t, "new", state.GitRevision)
}

func TestPollFailureUpdatesOnlyPollState(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{}, errors.New("git unavailable"))
	runner, store := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), t.TempDir())
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
	runner, store := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), t.TempDir())
	runner.Poll(t.Context())
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultNoChange, state.LastPollResult)
	assert.Empty(t, state.LastSyncResult)
}

func TestRunPullFailureUpdatesSyncState(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().Pull(gomock.Any()).Return(gitx.PullResult{}, errors.New("git unavailable"))
	runner, store := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), t.TempDir())
	runner.Run(t.Context(), Task{Reason: TriggerStartup})
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastSyncResult)
	assert.Equal(t, string(TriggerStartup), state.LastSyncReason)
	assert.Equal(t, "Git pull failed", state.LastSyncError)
}

func TestRunDiffFailureFallsBackToConfiguredOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	require.NoError(t, writeStacks(filepath.Join(repoDir, "stacks.yaml"), []string{"stack-a", "stack-b"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	repository.EXPECT().Diff(gomock.Any(), "old", "new").Return(nil, errors.New("diff failed"))
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	gomock.InOrder(
		reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
			Stack: config.StackSpec{Name: "stack-a", ComposeFile: "stack-a.yaml"}, Commit: "new",
		}).Return(nil),
		reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
			Stack: config.StackSpec{Name: "stack-b", ComposeFile: "stack-b.yaml"}, Commit: "new",
		}).Return(nil),
	)
	runner, store := newTestRunner(t, repository, reconciler, repoDir)
	runner.run(t.Context(), Task{Reason: TriggerPoll}, &gitx.PullResult{
		OldRevision: "old", NewRevision: "new", Updated: true,
	})
	assert.Equal(t, syncRunResultSuccess, readSQLState(t, store).LastSyncResult)
}

func TestRunConfigurationReloadFailurePersistsRevision(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(t.TempDir()).AnyTimes()
	runner, store := newTestRunner(t, repository, stackloop.NewMockStackReconciler(ctrl), t.TempDir())
	runner.run(t.Context(), Task{Reason: TriggerWebhook}, &gitx.PullResult{NewRevision: "revision"})
	state := readSQLState(t, store)
	assert.Equal(t, syncRunResultError, state.LastSyncResult)
	assert.Equal(t, "revision", state.GitRevision)
	assert.NotEmpty(t, state.LastSyncError)
}

func newTestRunner(
	t *testing.T, repository gitx.Repository, reconciler stackloop.StackReconciler, _ string,
) (*Runner, *modelstore.SQLStore) {
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
	return New(configuration, repository, metricGroup, events, store,
		networkCoordinator, reconciler, puller), store
}
