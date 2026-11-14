package stackloop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestSelfHealFailureRemainsPendingUntilVerifiedRecovery(t *testing.T) {
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	repository := gitx.NewMockRepository(ctrl)
	db, store := newSQLState(t)
	dir := t.TempDir()
	require.NoError(t, writeComposeFile(dir))
	desired, err := compose.NewFileLoader().Load(t.Context(), filepath.Join(dir, "app.yaml"))
	require.NoError(t, err)
	require.NoError(t, store.Update(t.Context(), func(runtime *model.Runtime) {
		runtime.Stacks["app"] = model.Stack{SourceDigest: desired.Digest}
	}))
	mockRepositoryFiles(repository, dir)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	var published []events.Event
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event events.Event) error {
		switch event.(type) {
		case *events.ServiceMissed, *events.ServiceDriftFixed:
			published = append(published, event)
		}
		return nil
	}).AnyTimes()
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir(),
		GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}}}}}
	group := metrics.NewGroup(metrics.CreateGroupParams{Namespace: "test"})
	registry := prometheus.NewRegistry()
	registry.MustRegister(group)
	one := uint64(1)
	live := []swarm.StackService{{Name: "api", Replicas: &one}}
	for _, stage := range []struct {
		name      string
		execution error
		readError error
		loadError bool
		initial   bool
		recovered bool
	}{
		{name: "accepted spec with confirmed task failure", execution: deployments.ErrConvergenceFailed, initial: true},
		{name: "live observation outage retains pending failure", loadError: true},
		{name: "verification read error preserves the incident", readError: assert.AnError},
		{name: "unknown recheck cannot establish recovery", execution: context.DeadlineExceeded},
		{name: "verified recovery clears pending failure", recovered: true},
	} {
		t.Run(stage.name, func(t *testing.T) {
			before := live
			if stage.initial {
				before = nil
			}
			if stage.loadError {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, assert.AnError)
			} else {
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(before, nil)
				stackDeployer.EXPECT().DeployService(gomock.Any(), gomock.Cond(func(req deployer.ServiceRequest) bool {
					return req.ServiceName == "api" && req.VerifyOnly == !stage.initial
				})).Return(deployer.Result{}, stage.execution)
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, stage.readError)
			}
			// Recreate the controller each tick; recovery must come from persisted state.
			reconciler := newTestReconciler(cfg, repository, deployments.NewService(db, publisher), stackDeployer,
				&swarm.Swarm{Services: manager}, publisher, group.Deploys, store, db)
			err = reconciler.Reconcile(t.Context(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "same",
			})
			if stage.recovered {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				if stage.execution != nil {
					assert.ErrorIs(t, err, stage.execution)
				}
			}
			state := readSQLState(t, store).Stacks["app"]
			assert.Equal(t, !stage.recovered, state.Services["api"].SelfHealPending)
			assert.Equal(t, !stage.recovered, state.DriftedServices["api"])
			if stage.recovered {
				assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), state.Services["api"].SyncStatus)
				assert.Empty(t, state.Services["api"].SyncError)
				assert.EqualValues(t, 1, deployMetricValue(t, registry, "app", "api", "success"))
			} else {
				assert.Equal(t, model.SyncStatus(model.SyncStatusOutOfSync), state.Services["api"].SyncStatus)
				assert.NotEmpty(t, state.Services["api"].SyncError)
				assert.Zero(t, deployMetricValue(t, registry, "app", "api", "success"))
				require.Len(t, published, 1, "no recovery event before successful verification")
				assert.False(t, published[0].(*events.ServiceMissed).Resolved)
			}
			// Pending convergence observation is allowed even after automatic healing is disabled.
			cfg.Spec.GitOps.Sync.Policy.SelfHeal = false
		})
	}
	require.Len(t, published, 2)
	assert.IsType(t, &events.ServiceDriftFixed{}, published[1])
	assert.EqualValues(t, 3, deployMetricValue(t, registry, "app", "api", "failed"))
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
	require.NoError(t, newTestReconciler(cfg, repository, deployments.NewService(db, publisher), stackDeployer,
		&swarm.Swarm{Services: manager}, publisher, group.Deploys, store, db).Reconcile(t.Context(), ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "same",
	}))
	assert.Len(t, published, 2, "healthy subsequent reconciliation does not repeat recovery")
	assert.EqualValues(t, 1, deployMetricValue(t, registry, "app", "api", "success"))
}
