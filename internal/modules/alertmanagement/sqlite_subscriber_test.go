package alertmanagement

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/deployment"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func newTestSQLSubscriber(store *modelstore.SQLStore) *SQLSubscriber {
	return NewSQLSubscriber(store, &dispatcher.NopDispatcher{}, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
}

func TestSQLSubscriberIdempotenceAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()
	event := events.Envelope{ID: "failed-1", OccurredAt: at,
		Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "app"}, Error: assert.AnError}}
	require.ErrorIs(t, db.WithinTransaction(ctx, func(ctx context.Context) error {
		require.NoError(t, subscriber.Handle(ctx, event))
		return assert.AnError
	}), assert.AnError)
	for range 2 {
		require.NoError(t, subscriber.Handle(ctx, event))
	}
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.EqualValues(t, 1, alerts[0].Occurrences)
	event.ID = "failed-2"
	event.OccurredAt = at.Add(time.Second)
	require.NoError(t, subscriber.Handle(ctx, event))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 2, alerts[0].Occurrences)
	recovered := events.Envelope{ID: "recovered", OccurredAt: at.Add(2 * time.Second),
		Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}}}
	require.NoError(t, subscriber.Handle(ctx, recovered))
	// A late duplicate of an old failure must not reopen the resolved incident.
	require.NoError(t, subscriber.Handle(ctx, event))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
}
func TestNodeAlertRecoveryIgnoresDelayedFailure(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now()
	disconnected := events.Envelope{ID: "001", OccurredAt: at, Event: &events.NodeDisconnected{NodeID: "worker"}}
	recovered := events.Envelope{ID: "003", OccurredAt: at.Add(time.Second), Event: &events.NodeConnected{NodeID: "worker"}}
	delayed := events.Envelope{ID: "002", OccurredAt: at.Add(time.Millisecond), Event: &events.NodeDisconnected{NodeID: "worker"}}
	for _, event := range []events.Envelope{disconnected, disconnected, recovered, delayed} {
		require.NoError(t, subscriber.Handle(ctx, event))
	}
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.ResourceTypeNode, alerts[0].ResourceType)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.EqualValues(t, 1, alerts[0].Occurrences)
}

func TestServiceDriftAlertRecoveryIgnoresRetriesAndDelayedDetection(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()
	drifted := events.Envelope{ID: "001", OccurredAt: at, Event: &events.ServiceDrifted{
		StackName: "payments", ServiceName: "api", Commit: "one", Reason: "replicas differ",
	}}
	fixed := events.Envelope{ID: "003", OccurredAt: at.Add(time.Second), Event: &events.ServiceDriftFixed{
		StackName: "payments", ServiceName: "api", Commit: "two",
	}}
	delayed := events.Envelope{ID: "002", OccurredAt: at.Add(time.Millisecond), Event: &events.ServiceMissed{
		StackName: "payments", ServiceName: "api", Commit: "old",
	}}
	for _, event := range []events.Envelope{drifted, drifted, fixed, delayed} {
		require.NoError(t, subscriber.Handle(ctx, event))
	}
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.Equal(t, uint64(1), alerts[0].Occurrences)
}

func TestPreparationAndInterruptedFailuresShareDeploymentAlertLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()
	for _, event := range []events.Envelope{
		{ID: "prepare-1", OccurredAt: at, Event: &events.DeployPreparationFailed{StackName: "app", ErrorCode: "preparation_failed"}},
		{ID: "prepare-2", OccurredAt: at.Add(time.Second), Event: &events.DeployPreparationFailed{StackName: "app", ErrorCode: "preparation_failed"}},
		{ID: "interrupted", OccurredAt: at.Add(2 * time.Second), Event: &events.DeployInterrupted{StackName: "app", Reason: "process_interrupted"}},
	} {
		require.NoError(t, subscriber.Handle(ctx, event))
	}
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.EqualValues(t, 3, alerts[0].Occurrences)
	assert.Equal(t, model.AlertStatusOpen, alerts[0].Status)
	assert.Equal(t, model.AlertKindSyncFailed, alerts[0].Kind)
	assert.Equal(t, "sync_failed:stack:app", alerts[0].Fingerprint)
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "success", OccurredAt: at.Add(3 * time.Second),
		Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}},
	}))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
}

func TestConfigurationChangedResolvesRemovedStackIdempotently(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(`
stacks:
  - name: kept
    composeFile: kept.yaml
`), 0o600))
	cfg := &config.Config{Spec: config.Spec{StacksSource: config.StacksSourceSpec{File: "stacks.yaml"}}}
	_, err = cfg.Reload(dir)
	require.NoError(t, err)
	subscriber := NewSQLSubscriber(repo, &dispatcher.NopDispatcher{},
		func(model.AlertKind) []events.AlertRecipient { return nil }, cfg)
	at := time.Now().UTC()

	for index, stack := range []string{"removed", "kept"} {
		require.NoError(t, subscriber.Handle(ctx, events.Envelope{
			ID: stack, OccurredAt: at.Add(time.Duration(index) * time.Second),
			Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: stack}, Error: assert.AnError},
		}))
		require.NoError(t, subscriber.Handle(ctx, events.Envelope{
			ID: "service-" + stack, OccurredAt: at.Add(time.Duration(index) * time.Second),
			Event: &events.ServiceDrifted{
				StackName: stack, ServiceName: "api", Reason: "service is missing",
			},
		}))
	}
	configurationChanged := events.Envelope{
		ID: "configuration", OccurredAt: at.Add(2 * time.Second), Event: &events.ConfigurationChanged{},
	}
	for range 2 {
		require.NoError(t, subscriber.Handle(ctx, configurationChanged))
	}

	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 4)
	byResource := map[string]model.Alert{}
	for _, alert := range alerts {
		byResource[alert.ResourceID] = alert
	}
	assert.Equal(t, model.AlertStatusResolved, byResource["removed"].Status)
	require.NotNil(t, byResource["removed"].Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, byResource["removed"].Resolution.Reason)
	assert.Equal(t, "configuration", byResource["removed"].Resolution.EventID)
	assert.Equal(t, model.AlertStatusOpen, byResource["kept"].Status)
	assert.Equal(t, model.AlertStatusResolved, byResource["removed/api"].Status)
	require.NotNil(t, byResource["removed/api"].Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, byResource["removed/api"].Resolution.Reason)
	assert.Equal(t, model.AlertStatusOpen, byResource["kept/api"].Status)
}

func TestTemporaryFailureCounterSurvivesRestartAndDuplicateDelivery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	at := time.Now().UTC()
	eventsBeforeRestart := []events.Envelope{
		{ID: "temporary-1", OccurredAt: at, Event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app"}, Error: temporaryError{message: "unavailable"},
		}},
		{ID: "temporary-2", OccurredAt: at.Add(time.Second), Event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app"}, Error: temporaryError{message: "unavailable"},
		}},
	}

	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	subscriber := newTestSQLSubscriber(modelstore.NewSQLStore(db))
	for _, event := range eventsBeforeRestart {
		require.NoError(t, subscriber.Handle(ctx, event))
		require.NoError(t, subscriber.Handle(ctx, event), "duplicate delivery must not increment the counter")
	}
	require.NoError(t, db.Close())

	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber = newTestSQLSubscriber(repo)
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "temporary-3", OccurredAt: at.Add(2 * time.Second), Event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app"}, Error: temporaryError{message: "unavailable"},
		},
	}))

	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, uint64(3), alerts[0].Occurrences)
	assert.Equal(t, "temporary-3", alerts[0].OpenEventID)
}

func TestGitRepositoryAuthenticationFailureOpensAndRecoveryResolvesAlert(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "failed", OccurredAt: at, Event: &events.GitPullFailed{
			Repository: "git.example.com/team/app", Category: events.GitFailureAuthentication,
			Description: "Git repository authentication failed", TriggerReason: "manual",
		},
	}))
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertKindGitRepositoryUnavailable, alerts[0].Kind)
	assert.Equal(t, model.ResourceTypeRepository, alerts[0].ResourceType)
	assert.Equal(t, "git.example.com/team/app", alerts[0].ResourceID)
	assert.Equal(t, "authentication", alerts[0].ErrorCode)
	assert.EqualValues(t, 1, alerts[0].Occurrences)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "recovered", OccurredAt: at.Add(time.Second), Event: &events.GitPullSucceeded{
			Repository: "git.example.com/team/app", TriggerReason: "startup",
		},
	}))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.Equal(t, model.ResolutionReasonRecovered, alerts[0].Resolution.Reason)
	assert.Equal(t, "recovered", alerts[0].Resolution.EventID)
}

func TestGitRepositoryChangeResolvesPreviousAsRemovedAndOrdersDelayedRecovery(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "old-failed", OccurredAt: at, Event: &events.GitPullFailed{
			Repository: "git.example.com/team/old", Category: events.GitFailureAuthentication,
			Description: "Git repository authentication failed",
		},
	}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "repository-changed", OccurredAt: at.Add(3 * time.Second), Event: &events.ConfigurationChanged{
			PreviousRepository: "git.example.com/team/old", Repository: "git.example.com/team/new",
		},
	}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "delayed-old-failure", OccurredAt: at.Add(time.Second), Event: &events.GitPullFailed{
			Repository: "git.example.com/team/old", Category: events.GitFailureAuthentication,
			Description: "Git repository authentication failed",
		},
	}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "delayed-old-success", OccurredAt: at.Add(2 * time.Second), Event: &events.GitPullSucceeded{
			Repository: "git.example.com/team/old",
		},
	}))
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "new-failed", OccurredAt: at.Add(4 * time.Second), Event: &events.GitPullFailed{
			Repository: "git.example.com/team/new", Category: events.GitFailureAuthentication,
			Description: "Git repository authentication failed",
		},
	}))

	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 2)
	byRepository := map[string]model.Alert{}
	for _, alert := range alerts {
		byRepository[alert.ResourceID] = alert
	}
	old := byRepository["git.example.com/team/old"]
	assert.Equal(t, model.AlertStatusResolved, old.Status)
	require.NotNil(t, old.Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, old.Resolution.Reason)
	assert.Equal(t, "repository-changed", old.Resolution.EventID)
	assert.Equal(t, model.AlertStatusOpen, byRepository["git.example.com/team/new"].Status)

	var oldCounters int
	require.NoError(t, db.Get(ctx, &oldCounters,
		"SELECT count(*) FROM alert_failure_streaks WHERE fingerprint = ?",
		model.GitRepositoryUnavailableFingerprint("git.example.com/team/old")))
	assert.Zero(t, oldCounters)
}

func TestGitRepositoryChangeBypassesUnreadyStackConfiguration(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	configStore := configstore.New(&config.Config{}, &dispatcher.NopDispatcher{})
	subscriber := NewSQLSubscriber(repo, &dispatcher.NopDispatcher{},
		func(model.AlertKind) []events.AlertRecipient { return nil }, configStore)
	at := time.Now().UTC()
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "old-failed", OccurredAt: at, Event: &events.GitPullFailed{
			Repository: "git.example.com/team/old", Category: events.GitFailureAuthentication,
			Description: "Git repository authentication failed",
		},
	}))
	require.NoError(t, repo.Create(ctx, model.Alert{
		ID: "stack-alert", Fingerprint: model.SyncFailedFingerprint("app"), Kind: model.AlertKindSyncFailed,
		ResourceType: model.ResourceTypeStack, ResourceID: "app", Status: model.AlertStatusOpen,
		Title: "Synchronization failed", Occurrences: 1, OpenedAt: at, UpdatedAt: at,
		OpenEventID: "stack-failed", LatestEventID: "stack-failed",
	}))

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "repository-changed", OccurredAt: at.Add(time.Second), Event: &events.ConfigurationChanged{
			PreviousRepository: "git.example.com/team/old", Repository: "git.example.com/team/new",
		},
	}))

	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 2)
	for _, alert := range alerts {
		if alert.ResourceType == model.ResourceTypeRepository {
			assert.Equal(t, model.AlertStatusResolved, alert.Status)
			require.NotNil(t, alert.Resolution)
			assert.Equal(t, model.ResolutionReasonResourceRemoved, alert.Resolution.Reason)
			continue
		}
		assert.Equal(t, model.AlertStatusOpen, alert.Status, "repository-only event must not reconcile stacks")
	}
}

func TestGitRepositoryNetworkThresholdSurvivesRestartAndDeduplicatesDelivery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	at := time.Now().UTC()
	db, err := storage.Open(ctx, dir)
	require.NoError(t, err)
	subscriber := newTestSQLSubscriber(modelstore.NewSQLStore(db))
	for index := range 2 {
		event := events.Envelope{
			ID: fmt.Sprintf("network-%d", index), OccurredAt: at.Add(time.Duration(index) * time.Second),
			Event: &events.GitPullFailed{
				Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
				Description: "Git repository is temporarily unreachable",
			},
		}
		require.NoError(t, subscriber.Handle(ctx, event))
		require.NoError(t, subscriber.Handle(ctx, event), "duplicate delivery must not increment the streak")
	}
	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "configuration", OccurredAt: at.Add(1500 * time.Millisecond), Event: &events.ConfigurationChanged{},
	}), "configuration changes must not reset repository failure streaks")
	require.NoError(t, db.Close())

	db, err = storage.Open(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber = newTestSQLSubscriber(repo)
	third := events.Envelope{
		ID: "network-2", OccurredAt: at.Add(2 * time.Second), Event: &events.GitPullFailed{
			Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
			Description: "Git repository is temporarily unreachable",
		},
	}
	require.NoError(t, subscriber.Handle(ctx, third))
	require.NoError(t, subscriber.Handle(ctx, third))
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.EqualValues(t, 3, alerts[0].Occurrences)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "network-3", OccurredAt: at.Add(3 * time.Second), Event: &events.GitPullFailed{
			Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
			Description: "Git repository is temporarily unreachable",
		},
	}))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1, "repeated failures must update one incident")
	assert.EqualValues(t, 4, alerts[0].Occurrences)

	require.NoError(t, subscriber.Handle(ctx, events.Envelope{
		ID: "network-recovered", OccurredAt: at.Add(4 * time.Second), Event: &events.GitPullSucceeded{
			Repository: "git.example.com/team/app",
		},
	}))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)

	var counters int
	require.NoError(t, db.Get(ctx, &counters, "SELECT count(*) FROM alert_failure_streaks"))
	assert.Zero(t, counters, "recovery must reset the network failure counter")
	for index := range 2 {
		require.NoError(t, subscriber.Handle(ctx, events.Envelope{
			ID:         fmt.Sprintf("network-after-recovery-%d", index),
			OccurredAt: at.Add(time.Duration(5+index) * time.Second),
			Event: &events.GitPullFailed{
				Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
				Description: "Git repository is temporarily unreachable",
			},
		}))
	}
	alerts, err = repo.List(ctx, modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err)
	assert.Empty(t, alerts, "a recovered network streak must start again below the threshold")
}

func TestGitRepositoryRecoveryIgnoresDelayedFailureAndResetsCounter(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(repo)
	at := time.Now().UTC()
	failed := events.Envelope{ID: "001", OccurredAt: at, Event: &events.GitPullFailed{
		Repository: "git.example.com/team/app", Category: events.GitFailureAuthentication,
		Description: "Git repository authentication failed",
	}}
	recovered := events.Envelope{ID: "003", OccurredAt: at.Add(2 * time.Second), Event: &events.GitPullSucceeded{
		Repository: "git.example.com/team/app",
	}}
	delayed := events.Envelope{ID: "002", OccurredAt: at.Add(time.Second), Event: &events.GitPullFailed{
		Repository: "git.example.com/team/app", Category: events.GitFailureAuthentication,
		Description: "Git repository authentication failed",
	}}
	for _, event := range []events.Envelope{failed, recovered, delayed} {
		require.NoError(t, subscriber.Handle(ctx, event))
	}
	alerts, err := repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.EqualValues(t, 1, alerts[0].Occurrences)

	var counters int
	require.NoError(t, db.Get(ctx, &counters, "SELECT count(*) FROM alert_failure_streaks"))
	assert.Zero(t, counters)
}

func TestGitNetworkDiagnosticReachesAlertAfterDurableCodec(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	store := modelstore.NewSQLStore(db)
	subscriber := newTestSQLSubscriber(store)
	const diagnostic = "Git repository connection refused"
	startedAt := time.Now().UTC()
	for i := range 3 {
		payload, encodeErr := codec.Encode(&events.GitPullFailed{
			Repository: "git.example.com/team/app", Category: events.GitFailureNetwork,
			Description: diagnostic,
		})
		require.NoError(t, encodeErr)
		decoded, decodeErr := codec.Decode(events.TypeNameGitPullFailed, codec.Version, payload)
		require.NoError(t, decodeErr)
		require.NoError(t, subscriber.Handle(ctx, events.Envelope{
			ID:         fmt.Sprintf("git-failure-%d", i),
			OccurredAt: startedAt.Add(time.Duration(i) * time.Second), Event: decoded,
		}))
	}
	alerts, err := store.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, diagnostic, alerts[0].Message)
	assert.EqualValues(t, 3, alerts[0].Occurrences)
	assert.Equal(t, diagnostic, alertSnapshot(alerts[0]).Message)
}

func TestDeploymentCompletionOutboxPreservesAlertThresholds(t *testing.T) {
	for _, phase := range []string{"apply", "verification", "cleanup"} {
		for _, temporary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/temporary=%t", phase, temporary), func(t *testing.T) {
				ctx := context.Background()
				dir := t.TempDir()
				db, err := storage.Open(ctx, dir)
				require.NoError(t, err)
				bus := eventbus.NewOutboxDispatcher(outbox.New(db))
				repo := modelstore.NewSQLStore(db)
				subscriber := newTestSQLSubscriber(repo)
				typ := events.TypeNameDeployFailed
				if phase == "verification" {
					typ = events.TypeNameDeployInterrupted
				}
				require.NoError(t, bus.Subscribe(typ, "alerts", subscriber))
				var deploymentID string
				for i := range 3 {
					service := deployment.NewService(db, bus)
					attempt, startErr := service.StartIfChanged(ctx, "app", fmt.Sprint(i), compose.File{
						Compose: compose.Compose{Services: compose.Services{{Name: "api", Image: "api:1"}}},
					}, true)
					require.NoError(t, startErr)
					if i == 0 {
						deploymentID = attempt.DeploymentID
					} else {
						assert.Equal(t, deploymentID, attempt.DeploymentID,
							"failed retries must keep one alert-correlated deployment")
					}
					var cause error = errors.New("token=private-value")
					if temporary {
						cause = fmt.Errorf("wrapped: %w", temporaryError{message: "token=private-value"})
					}
					switch phase {
					case "apply":
						err = service.FailApplyWithError(ctx, attempt.ID, gitopsmodel.Stack{}, "apply_failed", cause)
					case "verification":
						err = service.InterruptVerificationWithError(ctx, attempt.ID, gitopsmodel.Stack{}, cause)
					case "cleanup":
						err = service.FailCleanupWithError(ctx, attempt.ID, gitopsmodel.Stack{}, "prune_failed", time.Now(), cause)
					}
					require.NoError(t, err)
					stored, readErr := deploymentstore.NewStore(db).Get(ctx, attempt.DeploymentID)
					require.NoError(t, readErr)
					assert.NotContains(t, stored.ErrorDiagnostic, "private-value")
					assert.Contains(t, stored.ErrorDiagnostic, "****")
					var payload string
					require.NoError(t, db.Get(ctx, &payload, "SELECT payload FROM outbox_events WHERE event_type=?", typ))
					assert.NotContains(t, payload, "private-value")
					assert.Contains(t, payload, fmt.Sprintf(`"temporary":%t`, temporary))
					// Reopen before delivery: the subscriber must consume the persisted classification.
					require.NoError(t, db.Close())
					db, err = storage.Open(ctx, dir)
					require.NoError(t, err)
					bus = eventbus.NewOutboxDispatcher(outbox.New(db))
					repo = modelstore.NewSQLStore(db)
					subscriber = newTestSQLSubscriber(repo)
					require.NoError(t, bus.Subscribe(typ, "alerts", subscriber))
					processed, processErr := bus.ProcessNext(ctx)
					require.NoError(t, processErr)
					require.True(t, processed)
					alerts, listErr := repo.List(ctx, modelstore.ListFilter{})
					require.NoError(t, listErr)
					if temporary && i < 2 {
						assert.Empty(t, alerts)
					} else {
						require.Len(t, alerts, 1)
						assert.EqualValues(t, i+1, alerts[0].Occurrences)
						assert.NotContains(t, alerts[0].Message, "private-value")
					}
				}
				require.NoError(t, db.Close())
			})
		}
	}
}
