package alertmanagement

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

const (
	telegramA = "telegram:physical-a"
	telegramB = "telegram:physical-b"
)

func newNotificationSQLSubscriber(
	db *storage.Database, publisher interface {
		Publish(context.Context, events.Event) error
	},
) (*SQLSubscriber, *modelstore.SQLStore) {
	store := modelstore.NewSQLStore(db)
	return NewSQLSubscriber(store, publisher, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{}), store
}

func failureEnvelope(
	id string, sequence int64, at time.Time, key, channel string, source events.Type,
) events.Envelope {
	return events.Envelope{ID: id, PublicationSequence: sequence, OccurredAt: at,
		Event: &events.SendNotificationFailed{
			EventType: source, Destination: "telegram", Channel: channel, DestinationKey: key,
			Error: errors.New("handler_failed"),
		}}
}

func successEnvelope(
	id string, sequence int64, at time.Time, key, channel string, source events.Type,
) events.Envelope {
	return events.Envelope{ID: id, PublicationSequence: sequence, OccurredAt: at,
		Event: &events.NotificationSent{
			EventType: source, Destination: "telegram", Channel: channel, DestinationKey: key,
		}}
}

func TestNotificationDeliveryLifecycleByPhysicalDestination(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, bus)
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "opened-capture", &countingAlertHandler{}))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "resolved-capture", &countingAlertHandler{}))
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// Equal timestamps are independent, and aliases of one physical destination correlate.
	require.NoError(t, subscriber.Handle(t.Context(), failureEnvelope(
		"a-1", 1, at, telegramA, "infrastructure", events.TypeNodeJoined,
	)))
	require.NoError(t, subscriber.Handle(t.Context(), failureEnvelope(
		"a-2", 2, at, telegramA, "renamed-infrastructure", events.TypeNodeConnected,
	)))
	require.NoError(t, subscriber.Handle(t.Context(), failureEnvelope(
		"b-1", 3, at.Add(-time.Hour), telegramB, "audit", events.TypeNodeJoined,
	)))

	open, err := store.List(t.Context(), modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err)
	require.Len(t, open, 2)
	a := alertForFingerprint(t, open, model.NotificationDeliveryFailedFingerprint(telegramA))
	b := alertForFingerprint(t, open, model.NotificationDeliveryFailedFingerprint(telegramB))
	assert.EqualValues(t, 2, a.Occurrences)
	assert.Equal(t, "telegram:renamed-infrastructure", a.ResourceID)
	assert.EqualValues(t, 1, b.Occurrences)
	assert.Equal(t, 2, alertSignalCount(t, db, string(events.TypeNameAlertOpened)))

	// Success for B cannot recover A.
	require.NoError(t, subscriber.Handle(t.Context(), successEnvelope(
		"b-ok", 4, at.Add(-2*time.Hour), telegramB, "audit", events.TypeNodeJoined,
	)))
	a, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusOpen, a.Status)

	// Success for A resolves once. Repeated successes are no-ops for lifecycle publication.
	require.NoError(t, subscriber.Handle(t.Context(), successEnvelope(
		"a-ok", 5, at.Add(-3*time.Hour), telegramA, "renamed-infrastructure", events.TypeNodeJoined,
	)))
	require.NoError(t, subscriber.Handle(t.Context(), successEnvelope(
		"a-ok-again", 6, at.Add(-4*time.Hour), telegramA, "renamed-infrastructure", events.TypeNodeJoined,
	)))
	assert.Equal(t, 2, alertSignalCount(t, db, string(events.TypeNameAlertResolved)))

	// A later failure starts a new incident for A.
	require.NoError(t, subscriber.Handle(t.Context(), failureEnvelope(
		"a-3", 7, at.Add(-5*time.Hour), telegramA, "renamed-infrastructure", events.TypeNodeDisconnected,
	)))
	reopened, err := store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	assert.NotEqual(t, a.ID, reopened.ID)
	assert.EqualValues(t, 1, reopened.Occurrences)

	// Delayed success cannot resolve a newer failure, and a pre-recovery failure cannot reopen it later.
	require.NoError(t, subscriber.Handle(t.Context(), successEnvelope(
		"a-stale-success", 6, at.Add(10*time.Hour), telegramA, "renamed-infrastructure", events.TypeNodeJoined,
	)))
	_, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	require.NoError(t, subscriber.Handle(t.Context(), failureEnvelope(
		"a-delayed-old-failure", 4, at.Add(20*time.Hour), telegramA, "old-alias", events.TypeNodeJoined,
	)))
	reopened, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	assert.EqualValues(t, 1, reopened.Occurrences)
}

func TestConcurrentNotificationFailuresCreateOneAlert(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	store := modelstore.NewSQLStore(db)
	first := NewSubscriber(store, bus, func(model.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	second := NewSubscriber(store, bus, func(model.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "capture", &countingAlertHandler{}))
	at := time.Now().UTC()
	envelopes := []events.Envelope{
		failureEnvelope("failure-1", 1, at, telegramA, "ops", events.TypeNodeJoined),
		failureEnvelope("failure-2", 2, at, telegramA, "renamed", events.TypeNodeConnected),
	}

	var group sync.WaitGroup
	errorsCh := make(chan error, len(envelopes))
	for index, current := range []*Subscriber{first, second} {
		index, current := index, current
		group.Go(func() { errorsCh <- current.Handle(context.Background(), envelopes[index]) })
	}
	group.Wait()
	close(errorsCh)
	for err = range errorsCh {
		require.NoError(t, err)
	}

	alert, err := store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	assert.EqualValues(t, 2, alert.Occurrences)
	assert.Equal(t, 1, alertSignalCount(t, db, string(events.TypeNameAlertOpened)))
}

type failingNotificationHandler struct{}

func (failingNotificationHandler) Handle(context.Context, events.Envelope) error {
	return errors.New("private notification failure")
}

type successfulNotificationHandler struct{}

func (successfulNotificationHandler) Handle(context.Context, events.Envelope) error { return nil }

type countingAlertHandler struct {
	mu       sync.Mutex
	opened   int
	resolved int
}

func (h *countingAlertHandler) Handle(_ context.Context, envelope events.Envelope) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch envelope.Event.(type) {
	case *events.AlertOpened:
		h.opened++
	case *events.AlertResolved:
		h.resolved++
	}
	return nil
}

func (h *countingAlertHandler) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opened, h.resolved
}

func TestNotificationDeliveryLifecycleEndToEnd(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, bus)
	capture := &countingAlertHandler{}
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "notification",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "alert-delivery",
		outbox.ExternalNotification(successfulNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "capture", capture))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	drainOutbox(t, db, bus)
	alerts, err := store.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
	assert.Equal(t, model.ResolutionReasonRecovered, alerts[0].Resolution.Reason)
	_, resolved := capture.counts()
	assert.Equal(t, 1, resolved)
}

func TestAlertOpenedFailureDoesNotCreateRecursiveAlert(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, bus)
	capture := &countingAlertHandler{}
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "capture", capture))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "failed-alert-delivery",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Publish(t.Context(), failureEnvelope(
		"seed", 1, time.Now().UTC(), telegramA, "ops", events.TypeNodeJoined,
	).Event))

	drainOutbox(t, db, bus)
	alert, err := store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	require.NoError(t, err)
	assert.EqualValues(t, 2, alert.Occurrences)
	opened, _ := capture.counts()
	assert.Equal(t, 1, opened)
}

func TestAlertResolvedFailureDoesNotReopenSameIncident(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, bus)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "source-notification",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "recovery-notification",
		outbox.ExternalNotification(successfulNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "failed-resolution-delivery",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	drainOutbox(t, db, bus)
	_, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	assert.ErrorIs(t, err, modelstore.ErrAlertNotFound)
	resolved, err := store.List(t.Context(), modelstore.ListFilter{Status: model.AlertStatusResolved})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, model.ResolutionReasonRecovered, resolved[0].Resolution.Reason)
	assert.Equal(t, 0, outboxEventCount(t, db, events.TypeNameSendNotificationFailed))
}

func TestAlertResolvedFailureToAnotherDestinationRemainsObservable(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, bus)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "source-notification",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "recovery-notification",
		outbox.ExternalNotification(successfulNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "same-destination",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "other-destination",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "audit", telegramB)))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	drainOutbox(t, db, bus)
	_, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	assert.ErrorIs(t, err, modelstore.ErrAlertNotFound)
	alert, err := store.FindOpenByFingerprint(
		t.Context(), model.NotificationDeliveryFailedFingerprint(telegramB),
	)
	require.NoError(t, err)
	assert.EqualValues(t, 1, alert.Occurrences)
	assert.Equal(t, "telegram:audit", alert.ResourceID)
}

func TestAlertResolvedFailureCorrelationSurvivesRestart(t *testing.T) {
	directory := t.TempDir()
	db, err := storage.Open(t.Context(), directory)
	require.NoError(t, err)
	bus := outbox.New(db)
	subscriber, _ := newNotificationSQLSubscriber(db, bus)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, "source-notification",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameNotificationSent, "alert-management", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "recovery-notification",
		outbox.ExternalNotification(successfulNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "failed-resolution-delivery",
		outbox.ExternalNotification(failingNotificationHandler{}, "telegram", "ops", telegramA)))
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))

	for range 100 {
		_, err = db.Exec(t.Context(), "UPDATE outbox_deliveries SET available_at=0 WHERE status='pending'")
		require.NoError(t, err)
		var correlated int
		require.NoError(t, db.Get(t.Context(), &correlated, `SELECT count(*) FROM outbox_events
			WHERE event_type=? AND payload LIKE '%source_causation_id%'`, events.TypeNameSendNotificationFailed))
		if correlated > 0 {
			break
		}
		handled, processErr := bus.ProcessNext(t.Context())
		require.NoError(t, processErr)
		require.True(t, handled)
	}
	var correlated int
	require.NoError(t, db.Get(t.Context(), &correlated, `SELECT count(*) FROM outbox_events
		WHERE event_type=? AND payload LIKE '%source_causation_id%'`, events.TypeNameSendNotificationFailed))
	require.Equal(t, 1, correlated)
	require.NoError(t, db.Close())

	db, err = storage.Open(t.Context(), directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	restarted := outbox.New(db)
	subscriber, store := newNotificationSQLSubscriber(db, restarted)
	require.NoError(t, restarted.Subscribe(events.TypeNameSendNotificationFailed, "alert-management", subscriber))
	drainOutbox(t, db, restarted)

	_, err = store.FindOpenByFingerprint(t.Context(), model.NotificationDeliveryFailedFingerprint(telegramA))
	assert.ErrorIs(t, err, modelstore.ErrAlertNotFound)
	resolved, err := store.List(t.Context(), modelstore.ListFilter{Status: model.AlertStatusResolved})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
}

func drainOutbox(t *testing.T, db *storage.Database, bus *outbox.Bus) {
	t.Helper()
	for range 100 {
		_, err := db.Exec(t.Context(), "UPDATE outbox_deliveries SET available_at=0 WHERE status='pending'")
		require.NoError(t, err)
		handled, processErr := bus.ProcessNext(t.Context())
		require.NoError(t, processErr)
		if handled {
			continue
		}
		var active int
		require.NoError(t, db.Get(t.Context(), &active,
			"SELECT count(*) FROM outbox_deliveries WHERE status IN ('pending','processing')"))
		if active == 0 {
			return
		}
	}
	t.Fatal("outbox did not quiesce")
}

func alertForFingerprint(t *testing.T, alerts []model.Alert, fingerprint string) model.Alert {
	t.Helper()
	for _, alert := range alerts {
		if alert.Fingerprint == fingerprint {
			return alert
		}
	}
	t.Fatalf("alert %q not found", fingerprint)
	return model.Alert{}
}

func outboxEventCount(t *testing.T, db *storage.Database, eventType events.TypeName) int {
	t.Helper()
	var count int
	require.NoError(t, db.Get(t.Context(), &count,
		"SELECT count(*) FROM outbox_events WHERE event_type=?", eventType))
	return count
}
