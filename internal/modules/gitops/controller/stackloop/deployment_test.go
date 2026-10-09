package stackloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Thiht/transactor/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileDeploymentBoundaries(t *testing.T) {
	for _, scenario := range []string{"success and effective no-op", "prepare failure", "success commit failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			db, state := newSQLState(t)
			bus := outbox.New(db)
			hist, err := history.NewSQLStore(db, 50)
			require.NoError(t, err)
			require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "history", hist))
			repo := deployment.NewStore(db)
			ctrl := gomock.NewController(t)
			git := gitx.NewMockRepository(ctrl)
			docker := deployer.NewMockStackDeployer(ctrl)
			services := swarm.NewMockServiceManager(ctrl)
			dir := t.TempDir()
			git.EXPECT().WorkingDir().Return(dir).AnyTimes()
			cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, "data")}}
			r := New(cfg, git, docker, &swarm.Swarm{Services: services, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl)}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)
			req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "first"}
			if scenario == "prepare failure" {
				require.Error(t, r.Reconcile(ctx, req))
				attempts, err := repo.List(ctx, deployment.ListFilter{})
				require.NoError(t, err)
				assert.Empty(t, attempts)
				return
			}
			require.NoError(t, writeComposeFile(dir))
			docker.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, _ string, _ string, _ string, _ compose.Compose) error {
					assert.False(t, stdlib.IsWithinTransaction(ctx), "Docker must execute outside a DB write transaction")
					attempts, err := repo.List(ctx, deployment.ListFilter{})
					require.NoError(t, err)
					require.Len(t, attempts, 1)
					assert.Equal(t, deployment.Running, attempts[0].Status, "running must be committed before apply")
					if scenario == "success commit failure" {
						_, err = db.Get(ctx).ExecContext(ctx, "CREATE TRIGGER fail_event BEFORE INSERT ON outbox_events BEGIN SELECT RAISE(ABORT,'injected'); END")
						require.NoError(t, err)
					}
					return nil
				})
			services.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil).AnyTimes()
			err = r.Reconcile(ctx, req)
			if scenario == "success commit failure" {
				require.Error(t, err)
				attempts, err := repo.List(ctx, deployment.ListFilter{})
				require.NoError(t, err)
				require.Len(t, attempts, 1)
				assert.Equal(t, deployment.Running, attempts[0].Status)
				_, err = repo.Baseline(ctx, "app")
				require.ErrorIs(t, err, deployment.ErrNotFound)
				require.NoError(t, deployment.NewService(db, bus).InterruptRunning(ctx))
				interrupted, err := repo.Get(ctx, attempts[0].ID)
				require.NoError(t, err)
				assert.Equal(t, deployment.Interrupted, interrupted.Status)
				return
			}
			require.NoError(t, err)
			rows, err := hist.Read(ctx)
			require.NoError(t, err)
			assert.Empty(t, rows, "history is asynchronous")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("# formatting change only\nservices:\n  api:\n    image: nginx:latest\n"), 0600))
			req.Commit = "second"
			require.NoError(t, r.Reconcile(ctx, req))
			attempts, err := repo.List(ctx, deployment.ListFilter{})
			require.NoError(t, err)
			require.Len(t, attempts, 1)
			assert.Equal(t, deployment.Succeeded, attempts[0].Status)
			assert.Equal(t, "first", attempts[0].Commit)
		})
	}
}
