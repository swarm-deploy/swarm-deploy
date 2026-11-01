package stackloop

import (
	"fmt"
	"path/filepath"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileDeferredFailureProtectsUnresolvedServiceAndFailureProjection(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	dir := t.TempDir()
	require.NoError(t, writeComposeFile(dir))
	repository.EXPECT().WorkingDir().Return(dir).AnyTimes()
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, ".data"), Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}}}}
	bus := &dispatcher.NopDispatcher{}
	lifecycle := deployments.NewService(db, bus)
	swarmService := &swarm.Swarm{Services: manager}
	reconciler := newTestReconciler(cfg, repository, lifecycle, stackDeployer, swarmService, bus, &metrics.NopDeploys{}, stateStore, fs.NewLocalFileSystem(), db)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool { return req.StackName == "app" })).Return(deployer.Result{}, &pipe.StepError{StepName: "wait for service convergence", Err: fmt.Errorf("api paused: %w", waiter.ErrConvergenceFailed)})
	require.Error(t, reconciler.Reconcile(t.Context(), req))
	before := readSQLState(t, stateStore)
	assert.Equal(t, "convergence_failed", before.Stacks["app"].LastError)
	// Recreate both lifecycle and reconciler, keeping only SQLite state.
	reconciler = newTestReconciler(cfg, repository, deployments.NewService(db, bus), stackDeployer, swarmService, bus, &metrics.NopDeploys{}, stateStore, fs.NewLocalFileSystem(), db)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil).Times(5)
	for range 5 {
		require.NoError(t, reconciler.Reconcile(t.Context(), req))
	}
	assert.Equal(t, before.Stacks["app"].LastError, readSQLState(t, stateStore).Stacks["app"].LastError)
	assert.Equal(t, before.Stacks["app"].Services, readSQLState(t, stateStore).Stacks["app"].Services)
	stored, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, 1, stored.AttemptCount)
	assert.Equal(t, deploymentmodel.Failed, stored.Status)
	// Live state is observed, but the unresolved api is never automatically applied.
	req.IsManual = true
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
		return req.StackName == "app" &&
			gomock.Eq(deployer.StackOptions{RetryFailedUpdates: true, UnresolvedServices: []string{"api"}}).Matches(req.Options)
	})).Return(deployer.Result{}, nil)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
	require.NoError(t, reconciler.Reconcile(t.Context(), req))
	retried, _, err := deploymentstore.NewStore(db).Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, stored.ID, retried.ID)
	assert.Equal(t, 2, retried.AttemptCount)
	assert.Equal(t, deploymentmodel.Succeeded, retried.Status)
}
