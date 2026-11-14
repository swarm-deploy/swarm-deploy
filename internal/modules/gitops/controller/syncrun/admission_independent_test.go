package syncrun

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"go.uber.org/mock/gomock"
)

func TestFailedReloadResumesPreviouslyAcceptedPendingWork(t *testing.T) {
	ctrl := gomock.NewController(t)
	repoDir := t.TempDir()
	path := filepath.Join(repoDir, "stacks.yaml")
	require.NoError(t, writeStacks(path, []string{"running", "pending"}))
	repository := gitx.NewMockRepository(ctrl)
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	reconciler := stackloop.NewMockStackReconciler(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
		Stack: config.StackSpec{Name: "running", ComposeFile: "running.yaml"}, Commit: "A",
	}).DoAndReturn(func(context.Context, stackloop.ReconciliationRequest) error {
		close(started)
		<-release
		return nil
	})
	reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
		Stack: config.StackSpec{Name: "pending", ComposeFile: "pending.yaml"}, Commit: "A",
	}).Return(nil)
	runner, store, queue := newTestRunner(t, repository, reconciler, 1)
	runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "A", NewRevision: "A"})
	receive(t, started)
	require.NoError(t, os.Remove(path))
	runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "B", NewRevision: "B"})
	state := readSQLState(t, store)
	require.Equal(t, syncRunResultError, state.LastSyncResult)
	require.Equal(t, "A", state.GitRevision)
	releaseOnce.Do(func() { close(release) })
	waitIdle(t, queue)
}

func TestAdmissionClosedInsideConfigurationAcceptance(t *testing.T) {
	for _, change := range []string{"removed", "updated"} {
		t.Run(change, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repoDir := t.TempDir()
			path := filepath.Join(repoDir, "stacks.yaml")
			require.NoError(t, writeStacks(path, []string{"running", "pending"}))
			repository := gitx.NewMockRepository(ctrl)
			repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
			reconciler := stackloop.NewMockStackReconciler(ctrl)
			started, releaseRunning := make(chan struct{}), make(chan struct{})
			accepted, releaseReload := make(chan struct{}), make(chan struct{})
			var releaseRunningOnce, releaseReloadOnce sync.Once
			defer releaseRunningOnce.Do(func() { close(releaseRunning) })
			defer releaseReloadOnce.Do(func() { close(releaseReload) })
			reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, request stackloop.ReconciliationRequest) error {
					require.Equal(t, "running", request.Stack.Name)
					require.Equal(t, "A", request.Commit)
					close(started)
					<-releaseRunning
					return nil
				})
			latest := "pending"
			if change == "removed" {
				latest = "fresh"
			}
			reconciler.EXPECT().Reconcile(gomock.Any(), stackloop.ReconciliationRequest{
				Stack: config.StackSpec{Name: latest, ComposeFile: latest + ".yaml"}, Commit: "B",
			}).Return(nil)
			runner, _, queue := newTestRunner(t, repository, reconciler, 1)
			bus := dispatcher.NewMockDispatcher(ctrl)
			gomock.InOrder(
				bus.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil),
				bus.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
					func(context.Context, events.Event) error {
						// Config.Reload has already published the new snapshot, but
						// ConfigStore.Reload has not returned to the Runner yet.
						close(accepted)
						<-releaseReload
						return nil
					}),
			)
			runner.configStore = configstore.New(&config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{
				Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
			}}}, bus)
			runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "A", NewRevision: "A"})
			receive(t, started)
			require.NoError(t, writeStacks(path, []string{latest}))
			done := make(chan struct{})
			go func() {
				defer close(done)
				runner.run(t.Context(), Task{Reason: TriggerInterval}, &gitx.PullResult{OldRevision: "B", NewRevision: "B"})
			}()
			receive(t, accepted)
			require.Equal(t, latest, runner.configStore.Stacks()[0].Name)
			// This idle, unrelated stack would be admitted unless the queue's
			// admission gate was acquired before Reload accepted configuration B.
			assert.False(t, queue.SubmitCleanup(t.Context(), config.StackSpec{Name: "admission-probe"}, "A"))
			releaseRunningOnce.Do(func() { close(releaseRunning) })
			waitQueueState(t, runner, 1, 0)
			releaseReloadOnce.Do(func() { close(releaseReload) })
			receive(t, done)
			waitIdle(t, queue)
		})
	}
}
