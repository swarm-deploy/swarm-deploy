package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/pruner"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/gitpull"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/networkloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackqueue"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/syncrun"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDeploymentDeadlinePreservesGitOpsProgress(t *testing.T) {
	for _, parallelism := range []int{1, 2} {
		t.Run(fmt.Sprint(parallelism), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				db, err := storage.OpenSQLite(t.Context(), t.TempDir())
				require.NoError(t, err)
				defer db.Close()
				bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
				manager := swarm.NewMockServiceManager(ctrl)
				manager.EXPECT().ListServices(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				stackDeployer := deployer.NewMockStackDeployer(ctrl)
				stackDeployer.EXPECT().RefreshRegistryAuth(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				lifecycle := deployments.NewService(db, bus)
				executor := deployments.NewController(lifecycle, stackDeployer, manager,
					pruner.NewServicePruner(manager, bus, config.SyncPolicySpec{}), &metrics.NopDeploys{})
				slowStarted := make(chan struct{})
				stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
					return req.StackName == "slow"
				})).DoAndReturn(func(ctx context.Context, _ deployer.StackRequest) (deployer.Result, error) {
					close(slowStarted)
					<-ctx.Done()
					return deployer.Result{}, &pipe.StepError{StepName: "wait for service convergence", Err: ctx.Err()}
				})
				stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
					return req.StackName == "fast"
				})).Return(deployer.Result{}, nil)

				// Adapt prepared inputs to the real synchronous lifecycle. Queueing, deadlines,
				// completion transactions and retry decisions all use production components.
				reconciler := stackloop.NewMockStackReconciler(ctrl)
				var fastCompleted time.Time
				reconciler.EXPECT().Reconcile(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, req stackloop.ReconciliationRequest) error {
						result, executeErr := executor.Execute(ctx, deployments.ExecuteRequest{
							Stack: req.Stack, Commit: req.Commit, SourceChanged: true,
							Desired: compose.File{Path: req.Stack.ComposeFile, Compose: compose.Compose{
								Services: compose.Services{{Name: "api", Image: "example/api:1"}},
							}},
						})
						if req.Stack.Name == "fast" && result.Attempt != nil {
							fastCompleted = time.Now()
						}
						return executeErr
					}).AnyTimes()
				metricGroup := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "deadline_test"})
				queue := stackqueue.New(reconciler, metricGroup.Sync, parallelism)
				queue.Start(t.Context())
				defer queue.Stop()

				repoDir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(repoDir, "stacks.yaml"), []byte(`stacks:
  - name: slow
    composeFile: slow.yaml
  - name: fast
    composeFile: fast.yaml
`), 0o600))
				cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{
					Git: config.GitSpec{Repository: "https://example.com/infra.git", Pull: config.GitPullSpec{
						Interval: specw.Duration{Value: time.Minute},
					}},
					Sync: config.SyncSpec{Mode: config.SyncModeHybrid, Parallelism: parallelism,
						Interval: specw.Duration{Value: 2 * time.Minute}},
					Stacks: config.StacksSourceSpec{File: "stacks.yaml"},
				}}}
				configuration := configstore.New(cfg, bus)
				repository := gitx.NewMockRepository(ctrl)
				repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
				repository.EXPECT().Head(gomock.Any()).Return("r2", nil).AnyTimes()
				repository.EXPECT().Diff(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
				polls := 0
				repository.EXPECT().Pull(gomock.Any()).DoAndReturn(func(context.Context) (gitx.PullResult, error) {
					polls++
					if polls == 1 {
						return gitx.PullResult{NewRevision: "r1"}, nil
					}
					return gitx.PullResult{OldRevision: "r1", NewRevision: "r2", Updated: polls == 2}, nil
				}).AnyTimes()
				stateStore := modelstore.NewSQLStore(db)
				puller := gitpull.NewPuller(repository, gitpull.NewMonitor(db, stateStore, bus), metricGroup.Git, cfg.Spec.GitOps.Git)
				coordinator := networkloop.NewCoordinator(db, configuration, stateStore, networkloop.New(nil), bus)
				runner := syncrun.New(configuration, repository, metricGroup, bus, stateStore, coordinator, puller, queue)
				scheduler := New(cfg, runner)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				started := time.Now()
				go func() { assert.NoError(t, scheduler.Run(ctx, t.Context())) }()
				<-slowStarted
				time.Sleep(2*time.Minute + time.Second)
				synctest.Wait()
				assert.GreaterOrEqual(t, polls, 3, "Git polling continues during deployment")
				state, err := stateStore.Read(t.Context())
				require.NoError(t, err)
				assert.Equal(t, "r2", state.GitRevision, "new reconciliation requests are dispatched while slow is running")
				assert.Equal(t, string(syncrun.TriggerInterval), state.LastSyncReason, "periodic scheduling remains active")
				if parallelism == 2 {
					assert.False(t, fastCompleted.IsZero())
					assert.Less(t, fastCompleted.Sub(started), 15*time.Minute)
				} else {
					assert.True(t, fastCompleted.IsZero())
				}
				cancel() // Stop future dispatch; running stack work retains its execution context.
				synctest.Wait()
				require.NoError(t, queue.WaitIdle(t.Context()))
				assert.Equal(t, 15*time.Minute, time.Since(started))
				if parallelism == 1 {
					assert.Equal(t, started.Add(15*time.Minute), fastCompleted, "expiry releases the worker for the waiting stack")
				}
				store := deploymentstore.NewStore(db)
				slow, _, err := store.Latest(t.Context(), "slow")
				require.NoError(t, err)
				assert.Equal(t, deploymentmodel.Interrupted, slow.Status)
				assert.Equal(t, 1, slow.AttemptCount, "pending revisions must respect the retry backoff")
				fast, _, err := store.Latest(t.Context(), "fast")
				require.NoError(t, err)
				assert.Equal(t, deploymentmodel.Succeeded, fast.Status)
				if parallelism == 1 {
					assert.Equal(t, "r2", fast.Commit, "waiting work uses the latest dispatched revision")
				}
			})
		})
	}
}
