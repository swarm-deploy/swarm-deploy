package alertmanagement

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestStartupReloadResolvesOrphanedAlertWhenConfigurationIsUnchanged(t *testing.T) {
	dir := writeStackConfiguration(t, "kept")
	cfg := stackConfiguration()
	_, err := cfg.Reload(dir)
	require.NoError(t, err)
	db, repo := openAlertStore(t)
	seedOpenStackAlert(t, repo, cfg, "removed")
	bus := outbox.New(db)
	store := configstore.New(cfg, bus)
	subscriber := NewSQLSubscriber(repo, bus, noAlertRecipients, store)
	require.NoError(t, bus.Subscribe(events.TypeNameConfigurationChanged, "alert-management", subscriber))

	require.NoError(t, store.Reload(t.Context(), dir))
	require.NoError(t, store.Reload(t.Context(), dir))
	assertAlertStatus(t, repo, "removed", model.AlertStatusOpen)

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	alert := assertAlertStatus(t, repo, "removed", model.AlertStatusResolved)
	require.NotNil(t, alert.Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, alert.Resolution.Reason)
	handled, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	assert.False(t, handled, "startup reconciliation must only be published once")
}

func TestPendingConfigurationEventWaitsForInitialGitReload(t *testing.T) {
	dir := writeStackConfiguration(t, "kept")
	cfg := stackConfiguration() // Startup has no Git checkout and no accepted stacks.
	db, repo := openAlertStore(t)
	seedOpenStackAlert(t, repo, cfg, "kept")
	seedOpenStackAlert(t, repo, cfg, "removed")
	bus := outbox.New(db)
	store := configstore.New(cfg, bus)
	subscriber := NewSQLSubscriber(repo, bus, noAlertRecipients, store)
	require.NoError(t, bus.Subscribe(events.TypeNameConfigurationChanged, "alert-management", subscriber))

	// Simulate an Outbox event persisted by the previous process.
	require.NoError(t, bus.Publish(t.Context(), &events.ConfigurationChanged{}))
	require.False(t, store.Ready())
	require.Error(t, store.Reload(t.Context(), t.TempDir()), "missing Git files must not mark the store ready")

	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assertAlertStatus(t, repo, "kept", model.AlertStatusOpen)
	assertAlertStatus(t, repo, "removed", model.AlertStatusOpen)

	var deliveryStatus string
	require.NoError(t, db.Get(t.Context(), &deliveryStatus,
		"SELECT status FROM outbox_deliveries WHERE subscription_id = ?", "alert-management"))
	assert.Equal(t, "pending", deliveryStatus, "early delivery must be retried rather than acknowledged")

	// Once the actual Git configuration loads, the old event can safely run.
	require.NoError(t, store.Reload(t.Context(), dir))
	require.True(t, store.Ready())
	_, err = db.Exec(t.Context(),
		"UPDATE outbox_deliveries SET available_at = ? WHERE status = 'pending'",
		time.Now().Add(-time.Minute).UTC())
	require.NoError(t, err)
	handled, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assertAlertStatus(t, repo, "kept", model.AlertStatusOpen)
	resolved := assertAlertStatus(t, repo, "removed", model.AlertStatusResolved)
	require.NotNil(t, resolved.Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, resolved.Resolution.Reason)
}

func TestEmptyStacksReloadPublishesAndResolvesAllStackAlerts(t *testing.T) {
	nonEmptyDir := writeStackConfiguration(t, "app")
	emptyDir := writeStackConfiguration(t)
	cfg := stackConfiguration()
	_, err := cfg.Reload(nonEmptyDir)
	require.NoError(t, err)
	db, repo := openAlertStore(t)
	seedOpenStackAlert(t, repo, cfg, "app")
	bus := outbox.New(db)
	store := configstore.New(cfg, bus)
	subscriber := NewSQLSubscriber(repo, bus, noAlertRecipients, store)
	require.NoError(t, bus.Subscribe(events.TypeNameConfigurationChanged, "alert-management", subscriber))

	require.NoError(t, store.Reload(t.Context(), nonEmptyDir))
	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
	assertAlertStatus(t, repo, "app", model.AlertStatusOpen)

	require.NoError(t, store.Reload(t.Context(), emptyDir))
	handled, err = bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled, "transition to an empty stack list must publish configurationChanged")
	alert := assertAlertStatus(t, repo, "app", model.AlertStatusResolved)
	require.NotNil(t, alert.Resolution)
	assert.Equal(t, model.ResolutionReasonResourceRemoved, alert.Resolution.Reason)
}

func TestConcurrentReloadReadsAndOutboxConfigurationProcessing(t *testing.T) {
	nonEmptyDir := writeStackConfiguration(t, "app")
	emptyDir := writeStackConfiguration(t)
	cfg := stackConfiguration()
	_, err := cfg.Reload(nonEmptyDir)
	require.NoError(t, err)
	db, repo := openAlertStore(t)
	bus := outbox.New(db)
	store := configstore.New(cfg, bus)
	subscriber := NewSQLSubscriber(repo, bus, noAlertRecipients, store)
	require.NoError(t, bus.Subscribe(events.TypeNameConfigurationChanged, "alert-management", subscriber))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	start := make(chan struct{})
	reloadsDone := make(chan struct{})
	errorsChannel := make(chan error, 3)
	var processed atomic.Int64
	var group sync.WaitGroup
	group.Go(func() {
		<-start
		defer close(reloadsDone)
		for i := range 5 {
			dir := emptyDir
			if i%2 == 1 {
				dir = nonEmptyDir
			}
			if reloadErr := store.Reload(ctx, dir); reloadErr != nil {
				errorsChannel <- fmt.Errorf("reload desired configuration: %w", reloadErr)
				return
			}
		}
	})
	group.Go(func() {
		<-start
		for {
			_ = store.Stacks()
			select {
			case <-reloadsDone:
				return
			default:
				runtime.Gosched()
			}
		}
	})
	group.Go(func() {
		<-start
		for {
			handled, processErr := bus.ProcessNext(ctx)
			if processErr != nil {
				errorsChannel <- fmt.Errorf("process configuration event: %w", processErr)
				return
			}
			if handled {
				processed.Add(1)
				continue
			}
			select {
			case <-reloadsDone:
				return
			default:
				runtime.Gosched()
			}
		}
	})
	close(start)
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("concurrent configuration and outbox operations deadlocked")
	}
	close(errorsChannel)
	for operationErr := range errorsChannel {
		require.NoError(t, operationErr)
	}
	assert.Positive(t, processed.Load())
	assert.Empty(t, store.Stacks())
}

func stackConfiguration() *config.Config {
	return &config.Config{Spec: config.Spec{StacksSource: config.StacksSourceSpec{File: "stacks.yaml"}}}
}

func writeStackConfiguration(t *testing.T, stacks ...string) string {
	t.Helper()
	dir := t.TempDir()
	contents := "stacks: []\n"
	if len(stacks) > 0 {
		contents = "stacks:\n"
		for _, stack := range stacks {
			contents += "  - name: " + stack + "\n    composeFile: " + stack + ".yaml\n"
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(contents), 0o600))
	return dir
}

func openAlertStore(t *testing.T) (*storage.Database, *modelstore.SQLStore) {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, modelstore.NewSQLStore(db)
}

func seedOpenStackAlert(t *testing.T, repo *modelstore.SQLStore, stacks config.StackProvider, stack string) {
	t.Helper()
	subscriber := NewSQLSubscriber(repo, &dispatcher.NopDispatcher{}, noAlertRecipients, stacks)
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{
		ID: "seed-" + stack, OccurredAt: time.Now().UTC(),
		Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: stack}, Error: assert.AnError},
	}))
}

func assertAlertStatus(
	t *testing.T, repo *modelstore.SQLStore, resource string, status model.AlertStatus,
) model.Alert {
	t.Helper()
	alerts, err := repo.List(t.Context(), modelstore.ListFilter{ResourceID: resource})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, status, alerts[0].Status)
	return alerts[0]
}

func noAlertRecipients(model.AlertKind) []events.AlertRecipient { return nil }
