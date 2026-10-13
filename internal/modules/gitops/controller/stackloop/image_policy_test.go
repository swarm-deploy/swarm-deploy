package stackloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/deployer"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventhistory "github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	eventnotifications "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notifications"
	eventnotify "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestImagePolicyViolation(t *testing.T) {
	digest := "nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	latestDigest := "nginx:latest@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []struct {
		name   string
		policy config.ImagePolicySpec
		image  string
		want   string
	}{
		{name: "disabled allows malformed", image: "%%%"},
		{name: "required allows tag", policy: imagePolicy(true, false, false), image: "nginx:1.27"},
		{name: "required allows digest", policy: imagePolicy(true, false, false), image: digest},
		{name: "required rejects untagged", policy: imagePolicy(true, false, false), image: "nginx",
			want: imageViolationRequired},
		{name: "no latest allows explicit version", policy: imagePolicy(false, true, false), image: "nginx:1.27"},
		{name: "no latest allows digest", policy: imagePolicy(false, true, false), image: digest},
		{name: "no latest rejects explicit latest", policy: imagePolicy(false, true, false), image: "nginx:latest",
			want: imageViolationLatest},
		{name: "no latest rejects explicit latest with digest", policy: imagePolicy(false, true, false),
			image: latestDigest, want: imageViolationLatest},
		{name: "no latest rejects implicit latest", policy: imagePolicy(false, true, false), image: "nginx",
			want: imageViolationLatest},
		{name: "only sha rejects tag", policy: imagePolicy(false, false, true), image: "nginx:1.27",
			want: imageViolationOnlySHA},
		{name: "only sha allows digest", policy: imagePolicy(false, false, true), image: digest},
		{name: "enabled policy rejects malformed", policy: imagePolicy(true, false, false), image: "%%%",
			want: imageViolationInvalid},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, imagePolicyViolation(testCase.policy, testCase.image))
		})
	}
}

func TestCheckImagePolicyValidatesInitJobs(t *testing.T) {
	digest := "example/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	jobDigest := "example/migrate@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name   string
		policy config.ImagePolicySpec
		image  string
		code   string
	}{
		{name: "required", policy: imagePolicy(true, false, false), image: "example/migrate",
			code: imageViolationRequired},
		{name: "no latest", policy: imagePolicy(false, true, false), image: "example/migrate:latest",
			code: imageViolationLatest},
		{name: "only sha", policy: imagePolicy(false, false, true), image: "example/migrate:1.2.3",
			code: imageViolationOnlySHA},
		{name: "no latest allows digest only", policy: imagePolicy(false, true, false), image: jobDigest},
		{name: "disabled", image: "%%%"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			reconciler := &Reconciler{cfg: &config.Config{Spec: config.Spec{Sync: config.SyncSpec{
				Policy: config.SyncPolicySpec{Image: testCase.policy},
			}}}}
			err := reconciler.checkImagePolicy(t.Context(), &pipelinePayload{Desired: &compose.File{
				Path: "/repo/app.yaml",
				Compose: compose.Compose{Services: compose.Services{{
					Name: "api", Image: digest,
					InitJobs: []compose.InitJob{{Name: "migrate", Image: testCase.image}},
				}}},
			}})
			if testCase.code == "" {
				require.NoError(t, err)
				return
			}

			var validationErr *compose.ValidateComposeError
			require.ErrorAs(t, err, &validationErr)
			require.Len(t, validationErr.Issues, 1)
			assert.Equal(t, "service", validationErr.Issues[0].ResourceType)
			assert.Equal(t, "api", validationErr.Issues[0].ResourceName)
			assert.Equal(t, `x-init-deploy-jobs["migrate"].image`, validationErr.Issues[0].Field)
			assert.Equal(t, testCase.code, validationErr.Issues[0].Code)
			assert.Contains(t, validationErr.Issues[0].Message, `init job "migrate"`)
			assert.Contains(t, validationErr.Issues[0].Message, testCase.image)
		})
	}
}

func imagePolicy(required, noLatest, onlySHA bool) config.ImagePolicySpec {
	return config.ImagePolicySpec{Tag: config.ImageTagPolicySpec{
		Required: required, NoLatest: noLatest, OnlySHA: onlySHA,
	}}
}

func TestCheckImagePolicyReturnsAllStructuredIssues(t *testing.T) {
	reconciler := &Reconciler{cfg: &config.Config{Spec: config.Spec{Sync: config.SyncSpec{
		Policy: config.SyncPolicySpec{Image: imagePolicy(false, true, false)},
	}}}}
	err := reconciler.checkImagePolicy(t.Context(), &pipelinePayload{Desired: &compose.File{
		Path: "/repo/app.yaml",
		Compose: compose.Compose{Services: compose.Services{
			{Name: "api", Image: "nginx:latest", InitJobs: []compose.InitJob{
				{Name: "migrate", Image: "example/migrate:latest"},
			}},
			{Name: "worker", Image: "registry.example/worker", InitJobs: []compose.InitJob{
				{Name: "seed", Image: "example/seed"},
			}},
			{Name: "valid", Image: "registry.example/valid:1.2.3", InitJobs: []compose.InitJob{
				{Name: "prepare", Image: "example/prepare:1.2.3"},
			}},
		}},
	}})

	var validationErr *compose.ValidateComposeError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, "/repo/app.yaml", validationErr.FilePath)
	require.Len(t, validationErr.Issues, 4)
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "api", Field: "image", Code: imageViolationLatest,
		Message: `image "nginx:latest" must not use the latest tag`,
	}, validationErr.Issues[0])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "api", Field: `x-init-deploy-jobs["migrate"].image`,
		Code:    imageViolationLatest,
		Message: `init job "migrate": image "example/migrate:latest" must not use the latest tag`,
	}, validationErr.Issues[1])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "worker", Field: "image", Code: imageViolationLatest,
		Message: `image "registry.example/worker" must not use the latest tag`,
	}, validationErr.Issues[2])
	assert.Equal(t, compose.ValidationIssue{
		ResourceType: "service", ResourceName: "worker", Field: `x-init-deploy-jobs["seed"].image`,
		Code:    imageViolationLatest,
		Message: `init job "seed": image "example/seed" must not use the latest tag`,
	}, validationErr.Issues[3])
	assert.Contains(t, validationErr.Error(), `service "api": image:`)
	assert.Contains(t, validationErr.Error(), `service "api": x-init-deploy-jobs["migrate"].image:`)
	assert.Contains(t, validationErr.Error(), `service "worker": x-init-deploy-jobs["seed"].image:`)
}

func TestImagePolicyViolationLifecycle(t *testing.T) {
	ctx := context.Background()
	db, state := newSQLState(t)
	bus := outbox.New(db)
	alerts := alertstore.NewSQLStore(db)
	history, err := eventhistory.NewSQLStore(db, 10)
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{
		DataDir: filepath.Join(t.TempDir(), "data"),
		Sync: config.SyncSpec{Policy: config.SyncPolicySpec{
			Image: imagePolicy(false, true, false),
		}},
	}}
	subscriber := alertmanagement.NewSQLSubscriber(alerts, bus,
		func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, cfg)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "event-history", history))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameDeploySuccess, "event-history", history))

	ctrl := gomock.NewController(t)
	notifier := notifiers.NewMockNotifier(ctrl)
	notificationIDs := make([]string, 0, 2)
	notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, message notifiers.Message) (notifiers.Receipt, error) {
			payload, ok := message.Payload.(eventnotifications.DeploymentPayload)
			require.True(t, ok)
			assert.Equal(t, string(events.TypeNameSyncFailed), payload.Type)
			assert.Equal(t, eventnotifications.StatusPreparationFailed, payload.Status)
			assert.Equal(t, "validation", payload.Stage)
			notificationIDs = append(notificationIDs, payload.ID)
			return notifiers.Receipt{}, nil
		},
	).Times(2)
	require.NoError(t, bus.Subscribe(events.TypeNameDeployPreparationFailed, "notification:sync-failed",
		outbox.External(eventnotify.NewSubscriber(notifier))))

	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	repoDir := t.TempDir()
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	require.NoError(t, writeComposeFile(repoDir))
	reconciler := New(cfg, repository, stackDeployer, &swarm.Swarm{
		Services: serviceManager, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
	}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)

	err = reconciler.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "sha",
	})
	require.Error(t, err)
	var validationErr *compose.ValidateComposeError
	require.ErrorAs(t, err, &validationErr)
	require.Len(t, validationErr.Issues, 1)
	assert.Equal(t, imageViolationLatest, validationErr.Issues[0].Code)

	drainOutbox(t, ctx, bus)
	stored, err := alerts.List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, alertmodel.AlertStatusOpen, stored[0].Status)
	assert.Equal(t, uint64(1), stored[0].Occurrences)
	assert.Contains(t, stored[0].Message, "must not use the latest tag")
	firstAlertID := stored[0].ID
	deployments, err := deployment.NewStore(db).List(ctx, deployment.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, deployments.Deployments, "policy rejection must happen before attempt creation")
	historyEntries, err := history.Read(ctx)
	require.NoError(t, err)
	require.Len(t, historyEntries, 1)
	assert.Equal(t, events.TypeSyncFailed, historyEntries[0].Type)
	assert.Equal(t, "preparation_failed", historyEntries[0].Details["status"])
	assert.Equal(t, "validation", historyEntries[0].Details["stage"])

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
	require.Len(t, historyEntries, 2, "each failed synchronization must have one history entry")
	require.Len(t, notificationIDs, 2, "each failed synchronization must send one notification")
	assert.NotEqual(t, historyEntries[0].ID, historyEntries[1].ID)
	assert.NotEqual(t, notificationIDs[0], notificationIDs[1])
	assert.Equal(t, historyEntries[0].ID, notificationIDs[0], "history and notification must project one source event")
	assert.Equal(t, historyEntries[1].ID, notificationIDs[1], "history and notification must project one source event")

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"),
		[]byte("services:\n  api:\n    image: nginx:1.27\n"), 0o600))
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil)
	require.NoError(t, reconciler.Reconcile(ctx, ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "fixed",
	}))
	drainOutbox(t, ctx, bus)
	stored, err = alerts.List(ctx, alertstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, firstAlertID, stored[0].ID)
	assert.Equal(t, alertmodel.AlertStatusResolved, stored[0].Status)

	deployments, err = deployment.NewStore(db).List(ctx, deployment.ListFilter{})
	require.NoError(t, err)
	require.Len(t, deployments.Deployments, 1)
	assert.Equal(t, deployment.Succeeded, deployments.Deployments[0].Status)
	historyEntries, err = history.Read(ctx)
	require.NoError(t, err)
	require.Len(t, historyEntries, 3)
	assert.Equal(t, events.TypeDeploySuccess, historyEntries[2].Type)
}

func TestImagePolicyDisabledPreservesDeployment(t *testing.T) {
	db, state := newSQLState(t)
	bus := outbox.New(db)
	ctrl := gomock.NewController(t)
	repository := gitx.NewMockRepository(ctrl)
	stackDeployer := deployer.NewMockStackDeployer(ctrl)
	serviceManager := swarm.NewMockServiceManager(ctrl)
	repoDir := t.TempDir()
	repository.EXPECT().WorkingDir().Return(repoDir).AnyTimes()
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.yaml"), []byte(`services:
  api:
    image: nginx:latest
    x-init-deploy-jobs:
      - name: migrate
        image: example/migrate:latest
`), 0o600))
	stackDeployer.EXPECT().DeployStack(gomock.Any(), "app", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	serviceManager.EXPECT().ListStackServices(gomock.Any(), "app").Return([]swarm.StackService{}, nil)

	reconciler := New(&config.Config{Spec: config.Spec{DataDir: filepath.Join(t.TempDir(), "data")}}, repository,
		stackDeployer, &swarm.Swarm{
			Services: serviceManager, Configs: swarm.NewMockConfigManager(ctrl), Secrets: swarm.NewMockSecretManager(ctrl),
		}, bus, &metrics.NopDeploys{}, state, fs.NewLocalFileSystem(), db)

	require.NoError(t, reconciler.Reconcile(t.Context(), ReconciliationRequest{
		Stack: config.StackSpec{Name: "app", ComposeFile: "app.yaml"}, Commit: "sha",
	}))
	deployments, err := deployment.NewStore(db).List(t.Context(), deployment.ListFilter{})
	require.NoError(t, err)
	require.Len(t, deployments.Deployments, 1)
	assert.Equal(t, deployment.Succeeded, deployments.Deployments[0].Status)
}

func TestImagePolicyErrorsArePermanent(t *testing.T) {
	err := (&Reconciler{cfg: &config.Config{Spec: config.Spec{Sync: config.SyncSpec{Policy: config.SyncPolicySpec{
		Image: imagePolicy(false, true, false),
	}}}}}).checkImagePolicy(context.Background(), &pipelinePayload{Desired: &compose.File{
		Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "nginx:latest"}}},
	}})
	require.Error(t, err)
	var temporary interface{ Temporary() bool }
	assert.False(t, errors.As(err, &temporary))
}

func drainOutbox(t *testing.T, ctx context.Context, bus *outbox.Bus) {
	t.Helper()
	for {
		processed, err := bus.ProcessNext(ctx)
		require.NoError(t, err)
		if !processed {
			return
		}
	}
}
