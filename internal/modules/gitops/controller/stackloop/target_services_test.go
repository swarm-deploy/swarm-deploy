package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileTargetsUnappliedServiceRevisions(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	executor := deployer.NewMockStackDeployer(ctrl)
	db, store := newSQLState(t)
	dir := t.TempDir()
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	bus := &dispatcher.NopDispatcher{}
	create := func() *Reconciler {
		return newTestReconciler(cfg, repository, deployments.NewService(db, bus), executor, &swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, store, db)
	}
	var previous model.Stack
	for _, step := range []struct {
		name, api, worker, commit string
		targets                   []string
		fail, manual              bool
	}{
		{name: "initial unknown applied", api: "api:1", worker: "worker:1", commit: "one", targets: []string{"api", "worker"}},
		{name: "only api changes", api: "api:2", worker: "worker:1", commit: "two", targets: []string{"api"}},
		{name: "formatting only", api: "api:2", worker: "worker:1", commit: "three"},
		{name: "worker fails", api: "api:2", worker: "worker:2", commit: "four", targets: []string{"worker"}, fail: true},
		{name: "retry still targets unapplied worker", api: "api:2", worker: "worker:2", commit: "five", targets: []string{"worker"}, manual: true},
		{name: "api partially applies then fails", api: "api:3", worker: "worker:2", commit: "six", targets: []string{"api"}, fail: true},
		{name: "revert to last successful revision still applies", api: "api:2", worker: "worker:2", commit: "seven", targets: []string{"api"}},
	} {
		t.Run(step.name, func(t *testing.T) {
			manifest := fmt.Sprintf("# %s\nservices:\n  api:\n    image: %s\n  worker:\n    image: %s\n", step.commit, step.api, step.worker)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0600))
			if len(step.targets) > 0 {
				executor.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req deployer.StackRequest) (deployer.Result, error) {
					assert.Equal(t, step.targets, req.TargetServices)
					for _, name := range req.TargetServices {
						assert.True(t, readSQLState(t, store).Stacks["app"].Services[name].ApplyPending, "unconfirmed transition must survive a crash during external apply")
					}
					assert.Len(t, req.Desired.Services, 2, "execution keeps full desired for pruning")
					if step.fail {
						return deployer.Result{}, assert.AnError
					}
					return deployer.Result{}, nil
				})
			} else {
				executor.EXPECT().RefreshRegistryAuth(gomock.Any(), gomock.Any()).Return(nil)
			}
			if !step.fail {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{{Name: "api"}, {Name: "worker"}}, nil)
			}
			err := create().Reconcile(t.Context(), ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: step.commit, IsManual: step.manual})
			if step.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			current := readSQLState(t, store).Stacks["app"]
			if step.commit == "two" {
				assert.Equal(t, previous.Services["worker"], current.Services["worker"], "unselected service retains its applied revision and timestamp")
			}
			if step.fail {
				for name, service := range current.Services {
					assert.Equal(t, previous.Services[name].AppliedDigest, service.AppliedDigest)
					if name == step.targets[0] {
						assert.True(t, service.ApplyPending)
						assert.NotEqual(t, service.DesiredDigest, service.AppliedDigest)
					} else {
						assert.Equal(t, previous.Services[name], service)
					}
				}
			} else {
				for _, service := range current.Services {
					assert.Equal(t, service.DesiredDigest, service.AppliedDigest)
					assert.False(t, service.ApplyPending)
					assert.EqualValues(t, model.SyncStatusSynced, service.SyncStatus)
				}
			}
			if step.manual {
				assert.Equal(t, "four", current.Services["worker"].AppliedCommit, "retry records the revision that introduced the service change")
			}
			previous = current
		})
	}
}
