package stackloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDeferredDeploymentIsolatesIndependentReconciliation(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name    string
		global  bool
		stack   *bool
		service string
		heal    bool
	}{
		{name: "enabled", global: true, heal: true},
		{name: "global disabled"},
		{name: "stack disabled", global: true, stack: &no},
		{name: "stack enabled", stack: &yes, heal: true},
		{name: "service disabled", global: true, service: "false"},
		{name: "service enabled", stack: &no, service: "true", heal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repository := gitx.NewMockRepository(ctrl)
			manager := swarm.NewMockServiceManager(ctrl)
			stackDeployer := deployer.NewMockStackDeployer(ctrl)
			bus := dispatcher.NewMockDispatcher(ctrl)
			db, stateStore := newSQLState(t)
			dir := t.TempDir()
			manifest := "services:\n  a:\n    image: example/a:2\n  b:\n    image: example/b:1\n"
			if tc.service != "" {
				manifest += fmt.Sprintf("    deploy:\n      labels:\n        org.swarm-deploy.service.sync.policy.selfHeal: %q\n", tc.service)
			}
			manifest += "  c:\n    image: example/c:1\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0o600))
			repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
			cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, ".data"), Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: tc.global, Prune: true}}}}
			alerts := alertstore.NewSQLStore(db)
			subscriber := alertmanagement.NewSQLSubscriber(alerts, &dispatcher.NopDispatcher{}, func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
			var published []events.TypeName
			bus.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, e events.Event) error {
				published = append(published, e.Type().Name())
				return subscriber.Handle(ctx, events.Envelope{ID: fmt.Sprint(len(published)), OccurredAt: time.Now().UTC(), Event: e})
			}).AnyTimes()
			create := func() *Reconciler {
				return newTestReconciler(cfg, repository, deployments.NewService(db, bus), stackDeployer, &swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, stateStore, fs.NewLocalFileSystem(), db)
			}
			req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
			req.Stack.Sync.Policy.SelfHeal = tc.stack
			stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(deployer.Result{UnresolvedServices: []string{"a"}}, &pipe.StepError{StepName: "wait for service convergence", Err: waiter.ErrConvergenceFailed})
			require.Error(t, create().Reconcile(t.Context(), req))
			original := readSQLState(t, stateStore).Stacks["app"]
			failed, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
			require.NoError(t, err)
			// First observe healthy independent services. The orphan remains untouched even with prune enabled.
			healthy := []swarm.StackService{{Name: "a", UpdateStatus: &swarm.ServiceUpdateStatus{State: "paused"}}, {Name: "b"}, {Name: "c"}, {Name: "orphan"}}
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(healthy, nil)
			require.NoError(t, create().Reconcile(t.Context(), req))
			assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), readSQLState(t, stateStore).Stacks["app"].Services["b"].SyncStatus)
			// Then both a and b disappear: only b may be remediated. Recreate the process on every tick.
			missing := []swarm.StackService{{Name: "c"}, {Name: "orphan"}}
			for range 3 {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(missing, nil)
				if tc.heal {
					stackDeployer.EXPECT().DeployService(gomock.Any(), "app", gomock.Any(), "b", gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
					manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{{Name: "b"}, {Name: "c"}, {Name: "orphan"}}, nil)
				}
				require.NoError(t, create().Reconcile(t.Context(), req))
				state := readSQLState(t, stateStore).Stacks["app"]
				assert.Equal(t, original.Services["a"], state.Services["a"])
				assert.Equal(t, "convergence_failed", state.LastError)
				assert.Equal(t, original.LastDeployAt, state.LastDeployAt)
				want := model.SyncStatus(model.SyncStatusOutOfSync)
				if tc.heal {
					want = model.SyncStatusSynced
				}
				assert.Equal(t, want, state.Services["b"].SyncStatus)
				assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), state.Services["c"].SyncStatus)
			}
			after, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
			require.NoError(t, err)
			assert.Equal(t, deploymentmodel.Failed, after.Status)
			assert.Equal(t, failed, after, "maintenance cannot change the failed deployment or create Attempts")
			incident, err := alerts.FindOpenByFingerprint(t.Context(), alertmodel.SyncFailedFingerprint("app"))
			require.NoError(t, err)
			assert.Equal(t, alertmodel.AlertStatusOpen, incident.Status)
			assert.EqualValues(t, 1, incident.Occurrences)
			count := 0
			for _, typ := range published {
				if typ == events.TypeNameDeployFailed {
					count++
				}
				assert.NotEqual(t, events.TypeNameDeploySuccess, typ)
			}
			assert.Equal(t, 1, count)
			assert.Contains(t, published, events.TypeNameServiceMissed)
		})
	}
}
