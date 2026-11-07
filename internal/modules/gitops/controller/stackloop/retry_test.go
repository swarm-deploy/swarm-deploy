package stackloop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	pipe "github.com/artarts36/gopipe"
	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/services"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileRetriesExhaustedVersionConflictsAfterBackoff(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	dir := t.TempDir()
	require.NoError(t, writeComposeFile(dir))
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, ".data")}}
	bus := &dispatcher.NopDispatcher{}
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	newReconciler := func() *Reconciler {
		return newTestReconciler(cfg, repository, deployments.NewService(db, bus, deployments.WithClock(clock)),
			stackDeployer, &swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, stateStore, db)
	}
	desired := dockerswarm.ServiceSpec{
		Annotations:  dockerswarm.Annotations{Name: "app_api", Labels: map[string]string{labelsdict.StackNamespace: "app"}},
		TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "nginx:latest"}},
		Mode:         dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{}},
	}
	current := desired
	current.TaskTemplate.ContainerSpec = &dockerswarm.ContainerSpec{Image: "nginx:old"}
	applier := services.New(manager, &registry.NopAuthManager{})
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req deployer.StackRequest) (deployer.Result, error) {
			_, err := applier.Apply(ctx, req.StackName, []dockerswarm.ServiceSpec{desired}, services.ApplyOptions{})
			if err != nil {
				return deployer.Result{}, &pipe.StepError{StepName: "apply stack", Err: err}
			}
			return deployer.Result{}, nil
		}).Times(2)
	manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{
		ID: "api-id", Spec: current, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 1}},
	}, nil)
	for version := uint64(1); version <= 3; version++ {
		manager.EXPECT().Update(gomock.Any(), "api-id", dockerswarm.Version{Index: version}, desired, gomock.Any()).
			Return(swarm.ErrServiceConflict)
		manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{
			ID: "api-id", Spec: current, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: version + 1}},
		}, nil)
	}
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
	require.ErrorIs(t, newReconciler().Reconcile(t.Context(), req), swarm.ErrServiceConflict)
	store := deploymentstore.NewStore(db)
	failed, snapshot, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, deploymentmodel.Failed, failed.Status)
	assert.Equal(t, "apply_failed", failed.ErrorCode)
	assert.True(t, failed.Retryable)
	attempt, err := store.GetAttempt(t.Context(), failed.LatestAttemptID)
	require.NoError(t, err)
	assert.True(t, attempt.Retryable)

	// Restart with persisted state and the same automatic sync after the existing backoff.
	now = now.Add(time.Minute)
	manager.EXPECT().Inspect(gomock.Any(), desired.Name).Return(dockerswarm.Service{
		ID: "api-id", Spec: current, Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 4}},
	}, nil)
	manager.EXPECT().Update(gomock.Any(), "api-id", dockerswarm.Version{Index: 4}, desired, gomock.Any()).Return(nil)
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
	require.NoError(t, newReconciler().Reconcile(t.Context(), req))
	after, unchanged, err := store.Latest(t.Context(), "app")
	require.NoError(t, err)
	assert.Equal(t, failed.ID, after.ID)
	assert.Equal(t, snapshot.Digest, unchanged.Digest)
	assert.Equal(t, req.Commit, after.Commit)
	assert.Equal(t, 2, after.AttemptCount)
	assert.Equal(t, deploymentmodel.Succeeded, after.Status)
}

func TestReconcileDeferredFailureProtectsUnresolvedServiceAndFailureProjection(t *testing.T) {
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	manager := swarm.NewMockServiceManager(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	db, stateStore := newSQLState(t)
	dir := t.TempDir()
	require.NoError(t, writeComposeFile(dir))
	mockRepositoryFiles(repository, dir)
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, ".data"), GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}}}}}
	bus := &dispatcher.NopDispatcher{}
	lifecycle := deployments.NewService(db, bus)
	swarmService := &swarm.Swarm{Services: manager}
	reconciler := newTestReconciler(cfg, repository, lifecycle, stackDeployer, swarmService, bus, &metrics.NopDeploys{}, stateStore, db)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool { return req.StackName == "app" })).Return(deployer.Result{}, &pipe.StepError{StepName: "wait for service convergence", Err: fmt.Errorf("api paused: %w", waiter.ErrConvergenceFailed)})
	require.Error(t, reconciler.Reconcile(t.Context(), req))
	before := readSQLState(t, stateStore)
	assert.Equal(t, "convergence_failed", before.Stacks["app"].LastError)
	// Recreate both lifecycle and reconciler, keeping only SQLite state.
	reconciler = newTestReconciler(cfg, repository, deployments.NewService(db, bus), stackDeployer, swarmService, bus, &metrics.NopDeploys{}, stateStore, db)
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

func TestReconcileRetriesDockerUnavailableAfterBackoff(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, tc := range []struct {
			name      string
			status    int
			retryable bool
		}{
			{name: "unavailable", status: http.StatusServiceUnavailable, retryable: true},
			{name: "permanent", status: http.StatusBadRequest},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				repository := gitx.NewMockRepository(ctrl)
				manager := swarm.NewMockServiceManager(ctrl)
				stackDeployer := deployer.NewMockStackDeployer(ctrl)
				db, stateStore := newSQLState(t)
				dir := t.TempDir()
				require.NoError(t, writeComposeFile(dir))
				mockRepositoryFiles(repository, dir)
				cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(dir, ".data")}}
				bus := &dispatcher.NopDispatcher{}
				now := time.Now().UTC()
				clock := func() time.Time { return now }
				newReconciler := func() *Reconciler {
					return newTestReconciler(cfg, repository, deployments.NewService(db, bus, deployments.WithClock(clock)),
						stackDeployer, &swarm.Swarm{Services: manager}, bus, &metrics.NopDeploys{}, stateStore, db)
				}
				req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "one"}
				applyErr := dockerApplyError(t, operation, tc.status)
				if tc.retryable {
					require.ErrorIs(t, applyErr, cerrdefs.ErrUnavailable)
				}
				stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).Return(deployer.Result{},
					&pipe.StepError{StepName: "apply stack", Err: applyErr})
				require.Error(t, newReconciler().Reconcile(t.Context(), req))
				store := deploymentstore.NewStore(db)
				failed, desired, err := store.Latest(t.Context(), "app")
				require.NoError(t, err)
				assert.Equal(t, deploymentmodel.Failed, failed.Status)
				assert.Equal(t, "apply_failed", failed.ErrorCode)
				assert.Equal(t, tc.retryable, failed.Retryable)
				attempt, err := store.GetAttempt(t.Context(), failed.LatestAttemptID)
				require.NoError(t, err)
				assert.Equal(t, tc.retryable, attempt.Retryable)

				// A new reconciler and lifecycle use only persisted retry eligibility.
				now = now.Add(time.Minute - time.Nanosecond)
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
				require.NoError(t, newReconciler().Reconcile(t.Context(), req))
				deferred, _, err := store.Latest(t.Context(), "app")
				require.NoError(t, err)
				assert.Equal(t, 1, deferred.AttemptCount)

				now = now.Add(time.Nanosecond)
				if tc.retryable {
					stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).Return(deployer.Result{}, nil)
				}
				manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return(nil, nil)
				require.NoError(t, newReconciler().Reconcile(t.Context(), req))
				after, unchanged, err := store.Latest(t.Context(), "app")
				require.NoError(t, err)
				assert.Equal(t, failed.ID, after.ID)
				assert.Equal(t, desired.Digest, unchanged.Digest)
				assert.Equal(t, req.Commit, after.Commit)
				if tc.retryable {
					assert.Equal(t, 2, after.AttemptCount)
					assert.Equal(t, deploymentmodel.Succeeded, after.Status)
					assert.NotEqual(t, failed.LatestAttemptID, after.LatestAttemptID)
				} else {
					assert.Equal(t, 1, after.AttemptCount)
					assert.Equal(t, deploymentmodel.Failed, after.Status)
				}
			})
		}
	}
}

func dockerApplyError(t *testing.T, operation string, status int) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Docker API failure"}`, status)
	}))
	t.Cleanup(server.Close)
	dockerClient, err := client.NewClientWithOpts(client.WithHost(server.URL),
		client.WithHTTPClient(server.Client()), client.WithVersion("1.48"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, dockerClient.Close()) })
	manager := swarm.NewSwarm(dockerClient).Services
	spec := dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: "app_api"}}
	if operation == "create" {
		_, err = manager.Create(t.Context(), spec, dockerswarm.ServiceCreateOptions{})
	} else {
		err = manager.Update(t.Context(), "service-id", dockerswarm.Version{Index: 7}, spec,
			dockerswarm.ServiceUpdateOptions{})
	}
	require.Error(t, err)
	return err
}
