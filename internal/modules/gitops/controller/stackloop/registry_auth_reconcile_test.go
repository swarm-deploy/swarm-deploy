package stackloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockernetwork "github.com/docker/docker/api/types/network"
	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	gitmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	gitstore "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileRefreshesRegistryAuthWithoutNewDeployment(t *testing.T) {
	f := newCredentialReconcileFixture(t, "")
	require.NoError(t, f.reconcile())
	before := f.deployment()
	require.Equal(t, deploymentmodel.Succeeded, before.Status)
	service, tasks := *f.current, append([]swarm.ServiceTask(nil), f.tasks...)
	projection := readSQLState(t, f.state).Stacks["app"]
	require.NoError(t, f.reconcile())
	require.Zero(t, f.updates)
	f.rotate("second-synthetic-token")
	require.NoError(t, f.reconcile())
	require.Equal(t, 1, f.updates, "an unchanged Compose must still propagate rotated credentials")
	assert.True(t, f.storedToken == "second-synthetic-token", "Engine received the rotated synthetic token")
	assert.Equal(t, service.Spec.TaskTemplate, f.current.Spec.TaskTemplate)
	assert.Equal(t, tasks, f.tasks)
	for range 2 {
		require.NoError(t, f.reconcile())
	}
	assert.Equal(t, 1, f.updates, "unchanged credentials must not cause repeated writes")
	assert.Equal(t, before, f.deployment(), "maintenance must not create or alter Deployments or Attempts")
	assert.Equal(t, projection.LastDeployAt, readSQLState(t, f.state).Stacks["app"].LastDeployAt)
	assert.NotContains(t, f.published, events.TypeNameDeployPreparationFailed)
}

func TestReconcileRegistryAuthFailuresRetryWithoutLifecycleChanges(t *testing.T) {
	for _, failure := range []string{"lookup", "update"} {
		t.Run(failure, func(t *testing.T) {
			f := newCredentialReconcileFixture(t, "")
			require.NoError(t, f.reconcile())
			before := f.deployment()
			projection := readSQLState(t, f.state).Stacks["app"]
			f.rotate("second-synthetic-token")
			if failure == "lookup" {
				require.NoError(t, os.WriteFile(f.configPath, []byte(`{"auths":`), 0o600))
			} else {
				f.updateError = errors.New("synthetic Engine unavailable")
			}
			err := f.reconcile()
			require.Error(t, err, "credential maintenance failure must reach the reconciliation caller")
			assert.NotContains(t, err.Error(), "second-synthetic-token")
			assert.Equal(t, before, f.deployment())
			state := readSQLState(t, f.state).Stacks["app"]
			assert.Equal(t, projection.SourceDigest, state.SourceDigest)
			assert.Equal(t, projection.LastDeployAt, state.LastDeployAt)
			assert.Equal(t, gitmodel.SyncStatus(gitmodel.SyncStatusSynced), state.Services["api"].SyncStatus)
			assert.NotContains(t, f.published, events.TypeNameDeployPreparationFailed)
			f.updateError = nil
			f.rotate("second-synthetic-token")
			require.NoError(t, f.reconcile())
			assert.Equal(t, 1, f.updates, "a failed refresh must remain pending for the next tick")
			assert.Equal(t, before, f.deployment())
			require.NoError(t, f.reconcile())
			assert.Equal(t, 1, f.updates)
		})
	}
}

func TestReconcileAuthMaintenancePreservesUnsafeJobsAndDeferredAttempts(t *testing.T) {
	for _, scenario := range []string{"native job", "external one-shot", "failed", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			extra := ""
			if scenario == "native job" {
				extra = "      mode: replicated-job\n      restart_policy: {condition: none}\n"
			} else if scenario == "external one-shot" {
				extra = "      restart_policy: {condition: none}\n      labels:\n        org.swarm-deploy.service.sync.policy.replicasManaged: 'false'\n"
			}
			f := newCredentialReconcileFixture(t, extra)
			f.initialPaused = scenario == "failed"
			if scenario == "interrupted" {
				f.observationError = context.Canceled
			}
			err := f.reconcile()
			if scenario == "failed" || scenario == "interrupted" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			before := f.deployment()
			f.observationError = nil
			f.rotate("second-synthetic-token")
			require.NoError(t, f.reconcile())
			assert.Zero(t, f.updates, "maintenance must not execute unsafe work or bypass deployment backoff")
			assert.Equal(t, before, f.deployment())
		})
	}
}

func TestReconcileAuthConversionFailureDoesNotBlockMissingServiceRecovery(t *testing.T) {
	f := newCredentialReconcileFixture(t, "")
	require.NoError(t, f.reconcile())
	before := f.deployment()
	f.current, f.tasks = nil, nil
	f.networkError = errors.New("synthetic missing resource during auth conversion")
	f.rotate("second-synthetic-token")
	require.Error(t, f.reconcile(), "maintenance error must remain observable after normal drift processing")
	assert.Equal(t, 2, f.creates, "the missing service must still pass through ordinary self-heal")
	assert.NotNil(t, f.current)
	assert.Equal(t, gitmodel.SyncStatus(gitmodel.SyncStatusSynced), readSQLState(t, f.state).Stacks["app"].Services["api"].SyncStatus)
	assert.Contains(t, f.published, events.TypeNameServiceMissed)
	assert.NotContains(t, f.published, events.TypeNameDeployPreparationFailed)
	assert.Equal(t, before, f.deployment())
}

type credentialReconcileFixture struct {
	t                *testing.T
	reconciler       *Reconciler
	store            *deploymentstore.Store
	state            *gitstore.SQLStore
	configPath       string
	current          *dockerswarm.Service
	tasks            []swarm.ServiceTask
	storedToken      string
	creates, updates int
	initialPaused    bool
	updateError      error
	observationError error
	networkError     error
	published        []events.TypeName
}

func newCredentialReconcileFixture(t *testing.T, extra string) *credentialReconcileFixture {
	t.Helper()
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	authDir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", authDir)
	db, state := newSQLState(t)
	f := &credentialReconcileFixture{t: t, store: deploymentstore.NewStore(db), state: state,
		configPath: filepath.Join(authDir, "config.json")}
	f.rotate("first-synthetic-token")
	ctrl := gomock.NewController(t)
	manager := swarm.NewMockServiceManager(ctrl)
	networks := swarm.NewMockNetworkManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	secrets := swarm.NewMockSecretManager(ctrl)
	networks.EXPECT().Get(gomock.Any(), "app_default").DoAndReturn(func(context.Context, string, ...dockernetwork.InspectOptions) (swarm.Network, error) {
		if f.networkError != nil {
			err := f.networkError
			f.networkError = nil
			return swarm.Network{}, err
		}
		return swarm.Network{ID: "network-id", Name: "app_default", Scope: "swarm", Driver: "overlay",
			Labels: map[string]string{labelsdict.StackNamespace: "app"}}, nil
	}).AnyTimes()
	configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	manager.EXPECT().Inspect(gomock.Any(), "app_api").DoAndReturn(func(context.Context, string) (dockerswarm.Service, error) {
		if f.current == nil {
			return dockerswarm.Service{}, swarm.ErrServiceNotFound
		}
		return *f.current, nil
	}).AnyTimes()
	manager.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(f.createService).AnyTimes()
	manager.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(f.updateService).AnyTimes()
	manager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).DoAndReturn(f.liveServices).AnyTimes()
	manager.EXPECT().ListTasks(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string) ([]swarm.ServiceTask, error) {
		return f.tasks, f.observationError
	}).AnyTimes()
	bus := dispatcher.NewMockDispatcher(ctrl)
	bus.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event events.Event) error {
		f.published = append(f.published, event.Type().Name())
		return nil
	}).AnyTimes()
	repoDir := t.TempDir()
	replicas := "      replicas: 1\n"
	if strings.Contains(extra, "mode: replicated-job") {
		replicas = ""
	}
	manifest := "services:\n  api:\n    image: registry.example/api:1\n    command: [sleep, '600']\n    deploy:\n" + replicas + extra
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"), []byte(manifest), 0o600))
	repository := gitx.NewMockRepository(ctrl)
	mockRepositoryFiles(repository, repoDir)
	cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{SelfHeal: true}}}}}
	sw := &swarm.Swarm{Services: manager, Networks: networks, Configs: configs, Secrets: secrets}
	native := deployer.NewDeployer(time.Millisecond, time.Second, sw, &metrics.NopDeploys{},
		deployer.NewSecretResolver(deployer.PlaintextDecoder{}))
	f.reconciler = newTestReconciler(cfg, repository, deployments.NewService(db, bus), native, sw,
		bus, &metrics.NopDeploys{}, state, db)
	return f
}

func (f *credentialReconcileFixture) createService(_ context.Context, spec dockerswarm.ServiceSpec, options dockerswarm.ServiceCreateOptions) (string, error) {
	f.creates++
	// Model the SDK's JSON response, including omitted empty collections.
	wire, err := json.Marshal(spec)
	require.NoError(f.t, err)
	spec = dockerswarm.ServiceSpec{}
	require.NoError(f.t, json.Unmarshal(wire, &spec))
	now := time.Now().UTC()
	f.current = &dockerswarm.Service{ID: fmt.Sprintf("api-%d", f.creates), Spec: spec,
		Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 1}, CreatedAt: now, UpdatedAt: now}}
	if f.initialPaused {
		f.current.UpdateStatus = &dockerswarm.UpdateStatus{State: dockerswarm.UpdateStatePaused, StartedAt: &now}
	}
	f.tasks = []swarm.ServiceTask{{ID: fmt.Sprintf("task-%d", f.creates), CreatedAt: now,
		DesiredState: swarm.TaskStateRunning, CurrentState: swarm.TaskStateRunning, Spec: swarm.NewTaskSpec(spec.TaskTemplate)}}
	if spec.Mode.ReplicatedJob != nil {
		f.current.JobStatus = &dockerswarm.JobStatus{}
		f.tasks[0].CurrentState = swarm.TaskStateComplete
		f.tasks[0].JobIteration = &dockerswarm.Version{}
	}
	f.rememberToken(options.EncodedRegistryAuth)
	return f.current.ID, nil
}

func (f *credentialReconcileFixture) updateService(_ context.Context, id string, version dockerswarm.Version,
	spec dockerswarm.ServiceSpec, options dockerswarm.ServiceUpdateOptions,
) error {
	require.NotNil(f.t, f.current)
	require.Equal(f.t, f.current.ID, id)
	require.Equal(f.t, f.current.Version, version)
	require.Equal(f.t, f.current.Spec, spec, "credential maintenance must submit the exact current specification")
	require.False(f.t, options.QueryRegistry)
	if f.updateError != nil {
		return f.updateError
	}
	f.updates++
	previous := f.current.Spec
	f.current.PreviousSpec, f.current.Spec = &previous, spec
	f.current.Version.Index++
	f.current.UpdatedAt = time.Now().UTC()
	f.rememberToken(options.EncodedRegistryAuth)
	return nil
}

func (f *credentialReconcileFixture) rememberToken(encoded string) {
	auth, err := dockerregistry.DecodeAuthConfig(encoded)
	require.NoError(f.t, err)
	f.storedToken = auth.Password
}

func (f *credentialReconcileFixture) liveServices(context.Context, swarm.ListServicesFilter) ([]swarm.StackService, error) {
	if f.current == nil {
		return nil, nil
	}
	live := swarm.StackService{ID: f.current.ID, Name: "api", Mode: "replicated", ServiceSpec: f.current.Spec,
		CreatedAt: f.current.CreatedAt, UpdatedAt: f.current.UpdatedAt, Labels: f.current.Spec.Labels, DesiredTasks: 1}
	if f.current.Spec.Mode.Replicated != nil {
		live.Replicas = f.current.Spec.Mode.Replicated.Replicas
	}
	if f.current.Spec.Mode.ReplicatedJob != nil {
		live.Mode, live.JobStatus, live.CompletedTasks, live.DesiredTasks = "replicated-job", f.current.JobStatus, 1, 0
	}
	if f.current.UpdateStatus != nil {
		live.UpdateStatus = &swarm.ServiceUpdateStatus{State: swarm.ServiceUpdateState(f.current.UpdateStatus.State)}
		if f.current.UpdateStatus.StartedAt != nil {
			live.UpdateStatus.StartedAt = *f.current.UpdateStatus.StartedAt
		}
	}
	return []swarm.StackService{live}, nil
}

func (f *credentialReconcileFixture) rotate(token string) {
	f.t.Helper()
	config, err := json.Marshal(map[string]any{"auths": map[string]any{"registry.example": map[string]string{
		"username": "synthetic", "password": token,
	}}})
	require.NoError(f.t, err)
	require.NoError(f.t, os.WriteFile(f.configPath+".new", config, 0o600))
	require.NoError(f.t, os.Rename(f.configPath+".new", f.configPath))
}

func (f *credentialReconcileFixture) reconcile() error {
	f.t.Helper()
	return f.reconciler.Reconcile(f.t.Context(), ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "unchanged-commit",
	})
}

func (f *credentialReconcileFixture) deployment() deploymentmodel.Deployment {
	f.t.Helper()
	page, err := f.store.List(f.t.Context(), deploymentmodel.ListFilter{Stacks: []string{"app"}})
	require.NoError(f.t, err)
	require.Len(f.t, page.Deployments, 1)
	d, err := f.store.Get(f.t.Context(), page.Deployments[0].ID)
	require.NoError(f.t, err)
	require.Len(f.t, d.Attempts, 1)
	return d
}
