package alertmanagement

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotify "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.uber.org/mock/gomock"
)

const notificationTestSubscription = "notification:test:destination"

func TestTerminalNotificationFailureOpensAlert(t *testing.T) {
	for _, destination := range []string{"telegram", "custom"} {
		t.Run(destination, func(t *testing.T) {
			db, bus, alerts, notifier := notificationDeliveryPipeline(t, destination, "ops")
			notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(
				notifiers.Receipt{}, errors.New("token=private raw HTTP response"),
			).Times(12)
			require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))
			failed := exhaustDeliveryRetries(t, db, bus)

			var payload string
			require.NoError(t, db.Get(t.Context(), &payload,
				"SELECT payload FROM outbox_events WHERE event_type='OutboxDeliveryFailed'"))
			assert.NotContains(t, payload, "private")
			assert.NotContains(t, payload, "raw HTTP response")
			assert.Contains(t, payload, `"error_code":"handler_failed"`)

			processOne(t, bus)
			stored := onlyAlert(t, alerts)
			assert.Equal(t, model.AlertKindNotificationDeliveryFailed, stored.Kind)
			assert.Equal(t, model.ResourceTypeNotificationDestination, stored.ResourceType)
			assert.Equal(t, notificationTestSubscription, stored.ResourceID)
			assert.Equal(t, "handler_failed", stored.ErrorCode)
			assert.EqualValues(t, 1, stored.Occurrences)
			assert.NotContains(t, stored.Message, "private")
			assert.Equal(t, failed.EventID, sourceEventID(t, db, failed.EventID))
		})
	}
}

func TestTemporaryNotificationFailureThenSuccessDoesNotOpenAlert(t *testing.T) {
	db, bus, alerts, notifier := notificationDeliveryPipeline(t, "telegram", "ops")
	gomock.InOrder(
		notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, assert.AnError),
		notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, nil),
	)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))
	processOne(t, bus)
	makePendingDue(t, db)
	processOne(t, bus)

	stored, err := alerts.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, stored)
	assert.Zero(t, eventCount(t, db, events.TypeNameOutboxDeliveryFailed))
}

func TestMultipleNotificationFailuresAggregateByDestination(t *testing.T) {
	db, bus, alerts, notifier := notificationDeliveryPipeline(t, "custom", "audit")
	notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, assert.AnError).Times(24)
	for _, nodeID := range []string{"one", "two"} {
		require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: nodeID}))
	}
	exhaustDeliveryRetries(t, db, bus)
	exhaustDeliveryRetries(t, db, bus)
	processOne(t, bus)
	processOne(t, bus)

	stored := onlyAlert(t, alerts)
	assert.EqualValues(t, 2, stored.Occurrences)
	assert.Equal(t, model.NotificationDeliveryFailedFingerprint(notificationTestSubscription), stored.Fingerprint)
}

func TestSuccessfulReplayResolvesNotificationDeliveryAlert(t *testing.T) {
	db, bus, alerts, notifier := notificationDeliveryPipeline(t, "telegram", "ops")
	notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, assert.AnError).Times(12)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))
	failed := exhaustDeliveryRetries(t, db, bus)
	processOne(t, bus)
	assert.Equal(t, model.AlertStatusOpen, onlyAlert(t, alerts).Status)

	notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, nil)
	require.NoError(t, bus.Replay(t.Context(), failed.EventID, failed.SubscriptionID))
	assert.Equal(t, model.AlertStatusOpen, onlyAlert(t, alerts).Status,
		"replay scheduling must not resolve the incident")
	processOne(t, bus)
	assert.Equal(t, model.AlertStatusOpen, onlyAlert(t, alerts).Status,
		"successful delivery resolves only after its durable recovery signal is consumed")
	processOne(t, bus)

	stored := onlyAlert(t, alerts)
	assert.Equal(t, model.AlertStatusResolved, stored.Status)
	require.NotNil(t, stored.Resolution)
	assert.Equal(t, model.ResolutionReasonRecovered, stored.Resolution.Reason)
}

func TestDiscardResolvesNotificationDeliveryAlertExplicitly(t *testing.T) {
	db, bus, alerts, notifier := notificationDeliveryPipeline(t, "custom", "audit")
	notifier.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(notifiers.Receipt{}, assert.AnError).Times(12)
	require.NoError(t, bus.Publish(t.Context(), &events.NodeJoined{NodeID: "node"}))
	failed := exhaustDeliveryRetries(t, db, bus)
	processOne(t, bus)

	require.NoError(t, bus.Discard(t.Context(), failed.EventID, failed.SubscriptionID))
	assert.Equal(t, model.AlertStatusOpen, onlyAlert(t, alerts).Status)
	processOne(t, bus)

	stored := onlyAlert(t, alerts)
	assert.Equal(t, model.AlertStatusResolved, stored.Status)
	require.NotNil(t, stored.Resolution)
	assert.Equal(t, model.ResolutionReasonOperatorDiscarded, stored.Resolution.Reason)
}

func TestConcurrentNotificationFailureProjectionCreatesOneIncident(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := modelstore.NewSQLStore(db)
	first := newTestSQLSubscriber(store)
	second := newTestSQLSubscriber(store)
	at := time.Now().UTC()
	eventsToApply := []events.Envelope{
		{ID: "failure-one", OccurredAt: at, Event: notificationFailureEvent("source-one")},
		{ID: "failure-two", OccurredAt: at.Add(time.Millisecond), Event: notificationFailureEvent("source-two")},
	}

	var group sync.WaitGroup
	errorsCh := make(chan error, len(eventsToApply))
	for index, subscriber := range []*SQLSubscriber{first, second} {
		index, subscriber := index, subscriber
		group.Go(func() { errorsCh <- subscriber.Handle(context.Background(), eventsToApply[index]) })
	}
	group.Wait()
	close(errorsCh)
	for err = range errorsCh {
		require.NoError(t, err)
	}

	alerts, err := store.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Positive(t, alerts[0].Occurrences)
}

func notificationDeliveryPipeline(
	t *testing.T, destination, channel string,
) (*storage.Database, *outbox.Bus, *modelstore.SQLStore, *notifiers.MockNotifier) {
	t.Helper()
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	store := modelstore.NewSQLStore(db)
	alertSubscriber := newTestSQLSubscriber(store)
	for _, typ := range []events.TypeName{
		events.TypeNameOutboxDeliveryFailed,
		events.TypeNameOutboxDeliveryRecovered,
		events.TypeNameOutboxDeliveryDiscarded,
	} {
		require.NoError(t, bus.Subscribe(typ, "alert-management", alertSubscriber))
	}
	notifier := notifiers.NewMockNotifier(gomock.NewController(t))
	notificationSubscriber := eventnotify.NewSubscriber(notifier)
	require.NoError(t, bus.Subscribe(events.TypeNameNodeJoined, notificationTestSubscription,
		outbox.MonitorDelivery(outbox.External(notificationSubscriber), destination, channel)))
	return db, bus, store, notifier
}

func exhaustDeliveryRetries(t *testing.T, db *storage.Database, bus *outbox.Bus) outbox.FailedDelivery {
	t.Helper()
	for range 12 {
		processOne(t, bus)
		makePendingDue(t, db)
	}
	failed, err := bus.ListFailed(t.Context())
	require.NoError(t, err)
	for _, delivery := range failed {
		if delivery.SubscriptionID == notificationTestSubscription {
			return delivery
		}
	}
	t.Fatal("monitored delivery did not reach terminal failure")
	return outbox.FailedDelivery{}
}

func makePendingDue(t *testing.T, db *storage.Database) {
	t.Helper()
	_, err := db.Exec(t.Context(), "UPDATE outbox_deliveries SET available_at=0 WHERE status='pending'")
	require.NoError(t, err)
}

func processOne(t *testing.T, bus *outbox.Bus) {
	t.Helper()
	handled, err := bus.ProcessNext(t.Context())
	require.NoError(t, err)
	require.True(t, handled)
}

func onlyAlert(t *testing.T, store *modelstore.SQLStore) model.Alert {
	t.Helper()
	alerts, err := store.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	return alerts[0]
}

func eventCount(t *testing.T, db *storage.Database, typ events.TypeName) int {
	t.Helper()
	var count int
	require.NoError(t, db.Get(t.Context(), &count, "SELECT count(*) FROM outbox_events WHERE event_type=?", typ))
	return count
}

func notificationFailureEvent(sourceEventID string) *events.OutboxDeliveryFailed {
	return &events.OutboxDeliveryFailed{
		OutboxDelivery: events.OutboxDelivery{
			EventID: sourceEventID, SubscriptionID: notificationTestSubscription,
			EventType: events.TypeNameNodeJoined, Destination: "telegram", Channel: "ops",
		},
		ErrorCode: "handler_failed",
	}
}

func sourceEventID(t *testing.T, db *storage.Database, eventID string) string {
	t.Helper()
	var stored string
	require.NoError(t, db.Get(t.Context(), &stored, "SELECT id FROM outbox_events WHERE id=?", eventID))
	return stored
}
