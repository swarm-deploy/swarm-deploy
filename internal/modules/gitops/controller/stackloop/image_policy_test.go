package stackloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventhistory "github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/policies"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func imagePolicy(required, noLatest, onlySHA bool) config.ImagePolicySpec {
	return config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{
		Required: required, NoLatest: noLatest, OnlySHA: onlySHA,
	}}
}

func TestImagePolicyViolationLifecycle(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	alerts := alertstore.NewSQLStore(db)
	history, err := eventhistory.NewSQLStore(db, 10)
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{
		DataDir: filepath.Join(t.TempDir(), "data"),
		GitOps: config.GitOpsSpec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{
			Image: imagePolicy(false, true, false),
		}}},
	}}
	subscriber := alertmanagement.NewSQLSubscriber(alerts, bus,
		func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "event-history", history))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "event-history", history))

	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	repoDir := t.TempDir()
	mockRepositoryFiles(repository, repoDir)
	require.NoError(t, writeComposeFile(repoDir))
	reconciler := newTestReconciler(cfg, repository, deployments.NewService(db, bus), stackDeployer, &swarm.Swarm{
		Services: serviceManager, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, db)

	err = reconciler.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "sha",
	})
	require.Error(t, err)
	var validationErr *compose.ValidateComposeError
	require.ErrorAs(t, err, &validationErr)
	require.Len(t, validationErr.Issues, 1)
	assert.Equal(t, policies.ImageViolationLatest, validationErr.Issues[0].Code)

	drainOutbox(t, ctx, bus)
	stored, err := alerts.List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, alertmodel.AlertStatusOpen, stored[0].Status)
	assert.Equal(t, uint64(1), stored[0].Occurrences)
	assert.Contains(t, stored[0].Message, "must not use the latest tag")
	firstAlertID := stored[0].ID
	deployments, err := deploymentstore.NewStore(db).List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, deployments.Deployments, "policy rejection must happen before attempt creation")
	historyEntries, err := history.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, historyEntries, "preparation failures must remain alert-only")

	require.Error(t, reconciler.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "sha",
	}))
	drainOutbox(t, ctx, bus)
	stored, err = alerts.List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, stored, 1, "repeated violation must update the existing alert")
	assert.Equal(t, uint64(2), stored[0].Occurrences)
	historyEntries, err = history.Read(ctx)
	require.NoError(t, err)
	assert.Empty(t, historyEntries, "repeated preparation failures must remain alert-only")

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"),
		[]byte("services:\n  api:\n    image: nginx:1.27\n"), 0o600))
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Cond(func(req deployer.StackRequest) bool { return req.StackName == "app" })).Return(deployer.Result{}, nil)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{}, nil)
	require.NoError(t, reconciler.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "fixed",
	}))
	drainOutbox(t, ctx, bus)
	stored, err = alerts.List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, firstAlertID, stored[0].ID)
	assert.Equal(t, alertmodel.AlertStatusResolved, stored[0].Status)

	deployments, err = deploymentstore.NewStore(db).List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, deployments.Deployments, 1)
	assert.Equal(t, deploymentmodel.Succeeded, deployments.Deployments[0].Status)
	historyEntries, err = history.Read(ctx)
	require.NoError(t, err)
	require.Len(t, historyEntries, 1)
	assert.Equal(t, events.TypeDeploySuccess, historyEntries[0].Type)
}

func TestComposeLoadValidationLifecycle(t *testing.T) {
	ctx := t.Context()
	db, state := newSQLState(t)
	publications := outbox.New(db)
	bus := eventbus.NewOutboxDispatcher(publications)
	lastSync := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	previous := model.Stack{
		LastCommit: "previous", SourceDigest: "previous-digest", LastDeployAt: lastSync, StateUpdatedAt: lastSync,
		Status: model.StackStatus{Synced: 2},
		Services: map[string]model.Service{
			"swarm-deploy": {Image: "nginx:1.27", SyncStatus: model.SyncStatusSynced, SyncAt: lastSync},
			"worker":       {Image: "busybox:1.37", SyncStatus: model.SyncStatusSynced, SyncAt: lastSync},
		},
	}
	require.NoError(t, state.Update(ctx, func(runtime *model.Runtime) {
		runtime.Stacks["app"] = previous
		runtime.Stacks["other"] = previous
	}))
	alerts := alertstore.NewSQLStore(db)
	cfg := &config.Config{Spec: config.Spec{DataDir: filepath.Join(t.TempDir(), "data")}}
	subscriber := alertmanagement.NewSQLSubscriber(alerts, bus,
		func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "alert-management", subscriber))

	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	repoDir := t.TempDir()
	mockRepositoryFiles(repository, repoDir)
	path := filepath.Join(repoDir, "app.yaml")
	require.NoError(t, os.WriteFile(path, []byte("services: {swarm-deploy: {image: ghcr.io/company/MyApp:v1.2}}"), 0o600))
	const invalidImage = `image: invalid reference "ghcr.io/company/MyApp:v1.2"`
	reconciler := newTestReconciler(cfg, repository, deployments.NewService(db, bus), stackDeployer, &swarm.Swarm{
		Services: serviceManager, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, db)
	req := ReconciliationRequest{Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "invalid"}

	err := reconciler.Reconcile(ctx, req)
	var validation *compose.ValidateComposeError
	require.ErrorAs(t, err, &validation)
	assert.ErrorContains(t, err, "load compose")
	assert.Equal(t, req.Stack.ComposeFile, validation.FilePath)
	assert.ErrorContains(t, err, invalidImage)

	runtime := readSQLState(t, state)
	failed := runtime.Stacks["app"]
	assert.Equal(t, model.StackStatus{OutOfSynced: 2}, failed.Status)
	assert.Equal(t, req.Commit, failed.LastCommit)
	assert.Equal(t, "preparation_failed", failed.LastError)
	assert.True(t, failed.StateUpdatedAt.After(previous.StateUpdatedAt))
	assert.Equal(t, previous.SourceDigest, failed.SourceDigest)
	assert.Equal(t, previous.LastDeployAt, failed.LastDeployAt)
	assert.Equal(t, previous.DriftedServices, failed.DriftedServices)
	assert.Len(t, failed.Services, len(previous.Services))
	for name, service := range previous.Services {
		service.SyncStatus = model.SyncStatusOutOfSync
		assert.Equal(t, service, failed.Services[name], name)
	}
	assert.Equal(t, previous, runtime.Stacks["other"])

	published, err := publications.ListPublications(ctx, outbox.ListFilter{EventType: events.TypeNameDeployPreparationFailed})
	require.NoError(t, err)
	require.Len(t, published, 1)
	failure, ok := published[0].Envelope.Event.(*events.DeployPreparationFailed)
	require.True(t, ok)
	assert.Empty(t, failure.Services)

	attempts, err := deploymentstore.NewStore(db).List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, attempts.Deployments)

	drainOutbox(t, ctx, bus)
	alert, err := alerts.FindOpenByFingerprint(ctx, alertmodel.SyncFailedFingerprint("app"))
	require.NoError(t, err)
	require.NotNil(t, alert)
	assert.Equal(t, alertmodel.AlertKindSyncFailed, alert.Kind)
	assert.Equal(t, "validation", alert.FailureStage)
	assert.Contains(t, alert.Message, `validate compose file "app.yaml"`)
	assert.Contains(t, alert.Message, invalidImage)
	require.Len(t, alert.Findings, 1)
	assert.Equal(t, faults.CodeInvalidDesiredState, alert.Findings[0].Code)
	assert.Equal(t, "services.swarm-deploy: "+invalidImage, alert.Findings[0].Summary)

	require.NoError(t, os.WriteFile(path, []byte(`services:
  swarm-deploy:
    image: nginx:latest
    env_file: app.env
    x-init-deploy-jobs:
      - name: migrate
        image: example/migrate:latest
  worker:
    image: busybox:1.37
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.env"), []byte("MODE=production\n"), 0o600))
	stackDeployer.EXPECT().DeployStack(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, request deployer.StackRequest) (deployer.Result, error) {
			require.Len(t, request.Desired.Services, 2)
			service := request.Desired.Services[0]
			assert.Equal(t, "app", request.StackName)
			assert.Equal(t, "nginx:latest", service.Image)
			assert.Empty(t, service.EnvFiles)
			assert.Equal(t, "production", service.Environment.Map["MODE"])
			return deployer.Result{}, nil
		},
	)
	serviceManager.EXPECT().ListServices(gomock.Any(), swarm.ListServicesFilter{Stack: "app"}).Return([]swarm.StackService{}, nil)
	req.Commit = "fixed"
	require.NoError(t, reconciler.Reconcile(ctx, req))
	runtime = readSQLState(t, state)
	recovered := runtime.Stacks["app"]
	assert.Equal(t, model.StackStatus{Synced: 2}, recovered.Status)
	assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), recovered.ServiceSyncStatus("swarm-deploy"))
	assert.Equal(t, model.SyncStatus(model.SyncStatusSynced), recovered.ServiceSyncStatus("worker"))
	assert.Equal(t, previous, runtime.Stacks["other"])
	drainOutbox(t, ctx, bus)
	resolved, err := alerts.Get(ctx, alert.ID)
	require.NoError(t, err)
	assert.Equal(t, alertmodel.AlertStatusResolved, resolved.Status)

	attempts, err = deploymentstore.NewStore(db).List(ctx, deploymentmodel.ListFilter{})
	require.NoError(t, err)
	require.Len(t, attempts.Deployments, 1)
	assert.Equal(t, deploymentmodel.Succeeded, attempts.Deployments[0].Status)
}

func drainOutbox(t *testing.T, ctx context.Context, bus *eventbus.OutboxDispatcher) {
	t.Helper()
	for {
		processed, err := bus.ProcessNext(ctx)
		require.NoError(t, err)
		if !processed {
			return
		}
	}
}
