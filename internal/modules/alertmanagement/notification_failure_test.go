package alertmanagement

import (
	"context"
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

func TestNotificationFailuresUseOneGlobalAlert(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	store := modelstore.NewSQLStore(db)
	subscriber := NewSQLSubscriber(store, bus, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "capture", subscriber))
	at := time.Now().UTC()

	require.NoError(t, subscriber.Handle(t.Context(), notificationFailureEnvelope(
		"failure-1", at, events.TypeNodeJoined, "telegram", "ops",
	)))
	require.NoError(t, subscriber.Handle(t.Context(), notificationFailureEnvelope(
		"failure-2", at.Add(time.Millisecond), events.TypeAlertOpened, "telegram", "alerts",
	)))

	alerts, err := store.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.NotificationDeliveryFailedFingerprint(), alerts[0].Fingerprint)
	assert.Equal(t, model.AlertKindNotificationDeliveryFailed, alerts[0].Kind)
	assert.EqualValues(t, 2, alerts[0].Occurrences)
	assert.Contains(t, alerts[0].Message, "alerts")
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertOpened"),
		"failure to deliver AlertOpened must update the existing alert without opening another")
}

func TestConcurrentNotificationFailuresCreateOneAlert(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := outbox.New(db)
	store := modelstore.NewSQLStore(db)
	first := NewSubscriber(store, bus, func(model.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	second := NewSubscriber(store, bus, func(model.AlertKind) []events.AlertRecipient { return nil }, &config.Config{})
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "capture", first))
	at := time.Now().UTC()
	envelopes := []events.Envelope{
		notificationFailureEnvelope("failure-1", at, events.TypeNodeJoined, "telegram", "ops"),
		notificationFailureEnvelope("failure-2", at, events.TypeNodeConnected, "custom", "audit"),
	}

	var group sync.WaitGroup
	errorsCh := make(chan error, len(envelopes))
	for index, subscriber := range []*Subscriber{first, second} {
		index, subscriber := index, subscriber
		group.Go(func() { errorsCh <- subscriber.Handle(context.Background(), envelopes[index]) })
	}
	group.Wait()
	close(errorsCh)
	for err = range errorsCh {
		require.NoError(t, err)
	}

	alerts, err := store.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.EqualValues(t, 2, alerts[0].Occurrences)
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertOpened"))
}

func notificationFailureEnvelope(
	id string, at time.Time, eventType events.Type, destination, channel string,
) events.Envelope {
	return events.Envelope{ID: id, OccurredAt: at, Event: &events.SendNotificationFailed{
		EventType: eventType, Destination: destination, Channel: channel,
	}}
}
