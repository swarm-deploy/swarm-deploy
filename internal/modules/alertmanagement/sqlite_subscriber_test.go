package alertmanagement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

func TestSQLSubscriberIdempotenceAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	require.NoError(t, err)
	defer db.Close()
	repo := modelstore.NewSQLStore(db)
	subscriber := NewSQLSubscriber(repo)
	event := events.Envelope{ID: "failed-1", Event: &events.DeployFailed{DeployEvent: events.DeployEvent{StackName: "app"}, Error: assert.AnError}}
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
	require.NoError(t, subscriber.Handle(ctx, event))
	alerts, err = repo.List(ctx, modelstore.ListFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 2, alerts[0].Occurrences)
	recovered := events.Envelope{ID: "recovered", Event: &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}}}
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
	subscriber := NewSQLSubscriber(repo)
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
