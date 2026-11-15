package stackloop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	pipe "github.com/artarts36/gopipe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/preparation"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
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

func TestReconcileRevertAfterPersistingDesiredCreatesNewDeployment(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	executor := deployer.NewMockStackDeployer(ctrl)
	db, runtime := newSQLState(t)
	store := deploymentstore.NewStore(db)
	dir := t.TempDir()
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	bus := &dispatcher.NopDispatcher{}
	reconciler := newTestReconciler(cfg, repository, deployments.NewService(db, bus), executor,
		&swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, runtime, db)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}}
	reconcile := func(commit string, api string, targets []string) {
		t.Helper()
		req.Commit = commit
		manifest := fmt.Sprintf("services:\n  api:\n    image: %s\n  worker:\n    image: worker:1\n", api)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0600))
		executor.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool {
			return slices.Equal(targets, req.TargetServices)
		})).Return(deployer.Result{}, nil)
		manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).
			Return([]swarm.StackService{{Name: "api"}, {Name: "worker"}}, nil)
		require.NoError(t, reconciler.Reconcile(t.Context(), req))
	}
	reconcile("one", "api:1", []string{"api", "worker"})
	reconcile("two", "api:2", []string{"api"})
	original, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)

	// The process stops after persisting a worker change and before creating its Deployment.
	state := readSQLState(t, runtime).Stacks["app"]
	_, err = reconciler.persistDesiredServices(t.Context(), "app", "interrupted", map[string]string{
		"api": state.Services["api"].DesiredDigest, "worker": "changed-worker-digest",
	})
	require.NoError(t, err)

	reconcile("reverted", "api:2", []string{"worker"})
	latest, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, latest.ID)
	assert.Equal(t, deploymentmodel.Succeeded, latest.Status)
	require.Len(t, latest.Attempts, 1)
	assert.Equal(t, []string{"worker"}, latest.Attempts[0].TargetServices)
	after, err := store.Get(t.Context(), original.ID)
	require.NoError(t, err)
	assert.Equal(t, original, after)
}

func TestReconcilePreservesAppliedRevisionWhenDriftAnalysisFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	executor := deployer.NewMockStackDeployer(ctrl)
	db, runtime := newSQLState(t)
	store := deploymentstore.NewStore(db)
	dir := t.TempDir()
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	bus := &dispatcher.NopDispatcher{}
	reconciler := newTestReconciler(cfg, repository, deployments.NewService(db, bus), executor,
		&swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, runtime, db)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
	writeManifest := func(image string) {
		t.Helper()
		manifest := fmt.Sprintf("services:\n  api:\n    image: %s\n", image)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0600))
	}
	executor.EXPECT().DeployStack(gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil).Times(2)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).
		Return([]swarm.StackService{{Name: "api"}}, nil).Times(3)
	writeManifest("api:1")
	require.NoError(t, reconciler.Reconcile(t.Context(), req))
	previous := readSQLState(t, runtime).Stacks["app"].Services["api"].AppliedDigest
	require.NotEmpty(t, previous)

	writeManifest("api:2")
	req.Commit, req.IsManual = "two", true
	driftErr := errors.New("drift analysis unavailable")
	reconciler.pipeline = pipe.NewPipeline[*pipelinePayload]()
	reconciler.pipeline.Add(pipe.Step[*pipelinePayload]{Name: "execute deployment", Run: reconciler.executeDeployment})
	reconciler.pipeline.Add(pipe.Step[*pipelinePayload]{
		Name: "analyze drift",
		Run:  func(context.Context, *pipelinePayload) error { return driftErr },
	})
	reconcileErr := reconciler.Reconcile(t.Context(), req)
	require.ErrorIs(t, reconcileErr, driftErr)
	require.ErrorContains(t, reconcileErr, "analyze drift: drift analysis unavailable")
	stack := readSQLState(t, runtime).Stacks["app"]
	api := stack.Services["api"]
	assert.NotEqual(t, previous, api.AppliedDigest)
	assert.Equal(t, api.DesiredDigest, api.AppliedDigest)
	assert.Equal(t, "two", api.AppliedCommit)
	assert.False(t, api.ApplyPending)
	assert.Equal(t, "analyze_drift_failed", stack.LastError)
	completed, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Succeeded, completed.Status)
	require.Len(t, completed.Attempts, 1)
	assert.True(t, completed.Attempts[0].StartedAt.Equal(stack.LastDeployAt))
	assert.True(t, stack.LastDeployAt.Before(stack.StateUpdatedAt))

	reconciler.attachPipeline()
	req.IsManual = false
	executor.EXPECT().RefreshRegistryAuth(gomock.Any(), gomock.Any()).Return(nil)
	require.NoError(t, reconciler.Reconcile(t.Context(), req))
	latest, _, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, completed, latest)
}

func TestReconcileAdoptsLegacyDeploymentReceiptWithoutRedeploy(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	executor := deployer.NewMockStackDeployer(ctrl)
	db, store := newSQLState(t)
	dir := t.TempDir()
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	bus := &dispatcher.NopDispatcher{}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("services:\n  api:\n    image: api:1\n"), 0600))
	stack := config.StackSpec{Name: "app", ComposeFile: "app.yaml"}
	lifecycle := deployments.NewService(db, bus)
	reconciler := newTestReconciler(cfg, repository, lifecycle, executor,
		&swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, store, db)
	desired, err := reconciler.preparer.Load(t.Context(), stack, "legacy")
	require.NoError(t, err)
	prepared, err := reconciler.preparer.Prepare(t.Context(), preparation.Request{Stack: stack, Desired: desired})
	require.NoError(t, err)

	// A successful deployment recorded before per-service digests and receipts existed.
	attempt, err := lifecycle.StartIfChanged(t.Context(), "app", "legacy", *prepared, true)
	require.NoError(t, err)
	require.NoError(t, lifecycle.Complete(t.Context(), attempt.ID, deployments.Completion{Outcome: deployments.CompletionSucceeded}))
	_, err = db.Exec(t.Context(), "UPDATE deployments SET desired_payload = json_remove(desired_payload, '$.service_digests')")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(),
		"UPDATE deployment_attempts SET details_payload = json_remove(details_payload, '$.service_revisions', '$.target_services')")
	require.NoError(t, err)
	require.NoError(t, store.Update(t.Context(), func(runtime *model.Runtime) {
		runtime.Stacks["app"] = model.Stack{SourceDigest: desired.Digest, LastCommit: "legacy", Services: map[string]model.Service{
			"api": {Image: "api:1", SyncStatus: model.SyncStatusSynced},
		}}
	}))
	// DeployStack is not expected: the confirmed effective snapshot already matches.
	executor.EXPECT().RefreshRegistryAuth(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).
		Return([]swarm.StackService{{Name: "api"}}, nil)

	require.NoError(t, reconciler.Reconcile(t.Context(), ReconciliationRequest{Stack: stack, Commit: "legacy"}))

	api := readSQLState(t, store).Stacks["app"].Services["api"]
	assert.Equal(t, "legacy", api.AppliedCommit, "the deployed stack commit becomes the service commit")
	assert.NotEmpty(t, api.AppliedDigest)
	assert.Equal(t, api.DesiredDigest, api.AppliedDigest)
	assert.False(t, api.ApplyPending)
	assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), api.SyncStatus)
}

func TestRemovedServiceIsRedeployedAfterInterruptedReconciliation(t *testing.T) {
	db, store := newSQLState(t)
	applied := model.Service{
		DesiredDigest: "digest", AppliedDigest: "digest", AppliedCommit: "one", SyncStatus: model.SyncStatusSynced,
	}
	require.NoError(t, store.Update(t.Context(), func(runtime *model.Runtime) {
		runtime.Stacks["app"] = model.Stack{Services: map[string]model.Service{"api": applied, "worker": applied}}
	}))
	reconciler := &Reconciler{db: db, stateStore: store}

	// worker leaves Compose; the process stops after pruning, before the result is recorded.
	_, err := reconciler.persistDesiredServices(t.Context(), "app", "two", map[string]string{"api": "digest"})
	require.NoError(t, err)
	assert.NotContains(t, readSQLState(t, store).Stacks["app"].Services, "worker")

	// Restoring worker with its previous configuration selects it for deployment.
	_, err = reconciler.persistDesiredServices(t.Context(), "app", "three",
		map[string]string{"api": "digest", "worker": "digest"})
	require.NoError(t, err)
	restored := &compose.File{Compose: compose.Compose{Services: compose.Services{{Name: "api"}, {Name: "worker"}}}}
	targets, _ := targetServices(restored, readSQLState(t, store).Stacks["app"])
	assert.Equal(t, []string{"worker"}, targets)
}

func TestReconcileRestoresServiceAfterPartialPruneFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	executor := deployer.NewMockStackDeployer(ctrl)
	db, store := newSQLState(t)
	dir := t.TempDir()
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: t.TempDir()}}
	cfg.Spec.GitOps.Sync.Policy.Prune = true
	cfg.Spec.GitOps.Sync.Policy.SelfHeal = false
	bus := &dispatcher.NopDispatcher{}
	create := func() *Reconciler {
		return newTestReconciler(cfg, repository, deployments.NewService(db, bus), executor,
			&swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, store, db)
	}
	live := []swarm.StackService{
		{ID: "api-id", Name: "api", Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue}},
		{ID: "worker-id", Name: "worker", Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue}},
		{ID: "old-id", Name: "old", Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue}},
	}
	var previous model.Stack
	for _, step := range []struct {
		name              string
		services, targets []string
		removals          map[string]error
		fail              bool
	}{
		{
			name: "initial", services: []string{"api", "worker", "old"},
			targets: []string{"api", "worker", "old"},
		},
		{
			name: "remove-workers", services: []string{"api"},
			removals: map[string]error{"worker-id": nil, "old-id": assert.AnError}, fail: true,
		},
		{
			name: "restore-worker", services: []string{"api", "worker"}, targets: []string{"worker"},
			removals: map[string]error{"old-id": nil},
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			manifest := "services:\n"
			for _, name := range step.services {
				manifest += fmt.Sprintf("  %s:\n    image: %s:1\n", name, name)
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(manifest), 0600))
			executor.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, req deployer.StackRequest) (deployer.Result, error) {
				assert.ElementsMatch(t, step.targets, req.TargetServices, "a restored service must be deployed even with its original configuration")
				return deployer.Result{}, nil
			})
			manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(live, nil)
			for id, err := range step.removals {
				manager.EXPECT().Remove(gomock.Any(), id).Return(err)
			}

			err := create().Reconcile(t.Context(), ReconciliationRequest{
				Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: step.name,
			})
			if step.fail {
				require.ErrorIs(t, err, assert.AnError)
			} else {
				require.NoError(t, err)
			}
			current := readSQLState(t, store).Stacks["app"]
			assert.Len(t, current.Services, len(step.services), "failure handling must not restore removed service revisions")
			for _, name := range step.services {
				assert.Contains(t, current.Services, name)
				assert.NotEmpty(t, current.Services[name].AppliedDigest)
				assert.Equal(t, current.Services[name].DesiredDigest, current.Services[name].AppliedDigest)
				assert.False(t, current.Services[name].ApplyPending)
			}
			if step.name != "initial" {
				assert.Equal(t, previous.Services["api"], current.Services["api"], "unselected service retains its applied state")
			}
			previous = current
		})
	}
	assert.Equal(t, "restore-worker", previous.Services["worker"].AppliedCommit)
}
