package alertmanagement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.uber.org/mock/gomock"
)

func TestAlertLifecyclePublishesOnlyTransitions(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	repo := modelstore.NewSQLStore(db)
	subscriber := NewSQLSubscriber(repo, bus, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
	require.NoError(t, bus.Subscribe(events.TypeNameAlertOpened, "test-open", subscriber))
	require.NoError(t, bus.Subscribe(events.TypeNameAlertResolved, "test-resolve", subscriber))
	at := time.Now().UTC()
	failure := func(id string, offset time.Duration) events.Envelope {
		return events.Envelope{ID: id, OccurredAt: at.Add(offset), Event: &events.DeployFailed{
			DeployEvent: events.DeployEvent{StackName: "app"}, Error: errors.New("safe failure"),
		}}
	}
	require.NoError(t, subscriber.Handle(t.Context(), failure("failed-1", 0)))
	require.NoError(t, subscriber.Handle(t.Context(), failure("failed-1", 0)))
	require.NoError(t, subscriber.Handle(t.Context(), failure("failed-2", time.Second)))
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertOpened"))
	assert.Zero(t, alertSignalCount(t, db, "AlertResolved"))
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{ID: "success", OccurredAt: at.Add(2 * time.Second),
		Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}}}))
	assert.Equal(t, 1, alertSignalCount(t, db, "AlertResolved"))
	require.NoError(t, subscriber.Handle(t.Context(), failure("failed-3", 3*time.Second)))
	assert.Equal(t, 2, alertSignalCount(t, db, "AlertOpened"))
	alerts, err := repo.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 2)
	assert.NotEqual(t, alerts[0].ID, alerts[1].ID, "reopening must create a new lifecycle")
	assert.False(t, events.TypeNameAlertOpened.Valid())
	assert.False(t, events.TypeNameAlertResolved.Valid())
}

func TestServiceDriftEventsDriveAlertLifecycleThroughOutbox(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	bus := eventbus.NewOutboxDispatcher(outbox.New(db))
	repo := modelstore.NewSQLStore(db)
	subscriber := NewSQLSubscriber(repo, bus, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
	for _, typ := range []events.TypeName{
		events.TypeNameServiceMissed, events.TypeNameServiceDrifted, events.TypeNameServiceDriftFixed,
	} {
		require.NoError(t, bus.Subscribe(typ, "alert-management", subscriber))
	}

	require.NoError(t, bus.Publish(t.Context(), &events.ServiceDrifted{
		StackName: "payments", ServiceName: "api", Commit: "one", Reason: "replicas differ",
	}))
	processOutboxUntilIdle(t, bus)
	open, err := repo.List(t.Context(), modelstore.ListFilter{Status: model.AlertStatusOpen})
	require.NoError(t, err)
	require.Len(t, open, 1)

	require.NoError(t, bus.Publish(t.Context(), &events.ServiceDriftFixed{
		StackName: "payments", ServiceName: "api", Commit: "two",
	}))
	processOutboxUntilIdle(t, bus)

	alerts, err := repo.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, model.AlertStatusResolved, alerts[0].Status)
}

func processOutboxUntilIdle(t *testing.T, bus *eventbus.OutboxDispatcher) {
	t.Helper()
	for {
		processed, err := bus.ProcessNext(t.Context())
		require.NoError(t, err)
		if !processed {
			return
		}
	}
}

func TestAlertLifecyclePublicationRollback(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(assert.AnError)
	subscriber := NewSQLSubscriber(repo, publisher, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
	err = subscriber.Handle(t.Context(), events.Envelope{ID: "failed", OccurredAt: time.Now().UTC(),
		Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "app"}}})
	require.ErrorIs(t, err, assert.AnError)
	alerts, err := repo.List(t.Context(), modelstore.ListFilter{})
	require.NoError(t, err)
	assert.Empty(t, alerts)
	var projections int
	require.NoError(t, db.Get(t.Context(), &projections, "SELECT count(*) FROM projection_versions WHERE projection='alerts'"))
	assert.Zero(t, projections)
}

func TestAlertResolutionPublicationRollback(t *testing.T) {
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(nil)
	publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).Return(assert.AnError)
	subscriber := NewSQLSubscriber(repo, publisher, func(model.AlertKind) []events.AlertRecipient { return nil },
		&config.Config{})
	at := time.Now().UTC()
	require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{
		ID: "failed", OccurredAt: at,
		Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "app"}},
	}))
	err = subscriber.Handle(t.Context(), events.Envelope{
		ID: "success", OccurredAt: at.Add(time.Second),
		Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}},
	})
	require.ErrorIs(t, err, assert.AnError)
	alert, err := repo.FindOpenByFingerprint(t.Context(), model.SyncFailedFingerprint("app"))
	require.NoError(t, err)
	assert.Equal(t, model.AlertStatusOpen, alert.Status)
}

func alertSignalCount(t *testing.T, db *storage.Database, kind string) int {
	t.Helper()
	var count int
	require.NoError(t, db.Get(context.Background(), &count, "SELECT count(*) FROM outbox_events WHERE event_type=?", kind))
	return count
}
