package handlers

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

var testPublicationSequence atomic.Int64

func TestHandlerListEventsFiltersBySeverityAndCategory(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	require.NoError(t, storeEvent(store, context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "abc"},
	}))
	require.NoError(t, storeEvent(store, context.Background(), &events.UserAuthenticated{Username: "alice"}))
	require.NoError(t, storeEvent(store, context.Background(), &events.SendNotificationFailed{
		EventType: events.TypeDeploySuccess, Destination: "telegram", Channel: "ops", Error: errors.New("timeout"),
	}))

	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityError},
		Categories: []generated.EventCategory{generated.EventCategorySync},
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 1)
	assert.Equal(t, "sendNotificationFailed", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityError, resp.Events[0].Severity)
	assert.Equal(t, generated.EventCategorySync, resp.Events[0].Category)
}

func TestHandlerListEventsUsesOrWithinSeverityFilter(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	require.NoError(t, storeEvent(store, context.Background(), &events.SyncManualStarted{}))
	require.NoError(t, storeEvent(store, context.Background(), &events.SendNotificationFailed{
		EventType: events.TypeDeployFailed, Destination: "custom", Channel: "audit", Error: errors.New("down"),
	}))
	require.NoError(t, storeEvent(store, context.Background(), &events.AssistantPromptInjectionDetected{}))

	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityInfo, generated.EventSeverityError},
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 2)
	assert.Equal(t, "syncManualStarted", resp.Events[0].Type)
	assert.Equal(t, "sendNotificationFailed", resp.Events[1].Type)
}

func TestHandlerListEventsFiltersBySwarmCategory(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	require.NoError(t, storeEvent(store, context.Background(), &events.SyncManualStarted{}))
	require.NoError(t, storeEvent(store, context.Background(), &events.NodeJoined{
		NodeID: "node-1", NodeName: "worker-1",
	}))

	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Categories: []generated.EventCategory{generated.EventCategorySwarm},
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 1)
	assert.Equal(t, "nodeJoined", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityInfo, resp.Events[0].Severity)
	assert.Equal(t, generated.EventCategorySwarm, resp.Events[0].Category)
}

func TestHandlerListEventsFiltersByType(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	require.NoError(t, storeEvent(store, context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "abc"},
	}))
	require.NoError(t, storeEvent(store, context.Background(), &events.NodeJoined{
		NodeID: "node-1", NodeName: "worker-1",
	}))
	require.NoError(t, storeEvent(store, context.Background(), &events.UserAuthenticated{Username: "alice"}))

	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Types: []string{string(events.TypeNameNodeJoined)},
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 1)
	assert.Equal(t, "nodeJoined", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityInfo, resp.Events[0].Severity)
}

func TestHandlerListEventsFiltersBySince(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	since := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	require.NoError(t, storeEventAt(store, context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "old", Commit: "old"},
	}, since.Add(-time.Second)))
	for _, item := range []struct {
		at          time.Time
		destination string
	}{{since, "boundary"}, {since.Add(time.Second), "new"}} {
		require.NoError(t, storeEventAt(store, context.Background(), &events.SendNotificationFailed{
			EventType: events.TypeDeploySuccess, Destination: item.destination, Channel: "ops", Error: errors.New("down"),
		}, item.at))
	}

	var generatedSince generated.OptDateTime
	generatedSince.SetTo(since)
	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityError},
		Since: generatedSince,
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 2)
	assert.Equal(t, "sendNotificationFailed", resp.Events[0].Type)
	assert.Equal(t, "sendNotificationFailed", resp.Events[1].Type)
}

func TestHandlerListEventsLimitsLatestFilteredEvents(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	for _, commit := range []string{"abc", "def", "ghi"} {
		require.NoError(t, storeEvent(store, context.Background(), &events.DeploySuccess{
			DeployEvent: events.DeployEvent{StackName: "api", Commit: commit},
		}))
	}
	require.NoError(t, storeEvent(store, context.Background(), &events.UserAuthenticated{Username: "alice"}))

	var limit generated.OptInt32
	limit.SetTo(2)
	resp, err := (&handler{history: store}).ListEvents(context.Background(), generated.ListEventsParams{
		Types: []string{string(events.TypeNameDeploySuccess)},
		Limit: limit,
	})
	require.NoError(t, err)
	require.Len(t, resp.Events, 2)
	assert.Equal(t, "def", resp.Events[0].Details.Value["commit"])
	assert.Equal(t, "ghi", resp.Events[1].Details.Value["commit"])
}

func TestHandlerListEventsCursorPagination(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	for _, stack := range []string{"one", "two", "three", "four"} {
		require.NoError(t, storeEvent(store, context.Background(), &events.DeploySuccess{
			DeployEvent: events.DeployEvent{StackName: stack, Commit: stack},
		}))
	}

	h := &handler{history: store}
	params := generated.ListEventsParams{}
	params.Limit.SetTo(2)
	params.Sort.SetTo("time")
	params.Order.SetTo("desc")

	first, err := h.ListEvents(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, first.Events, 2)
	assert.Equal(t, "four", first.Events[0].Details.Value["stack"])
	assert.Equal(t, "three", first.Events[1].Details.Value["stack"])
	cursor, ok := first.NextCursor.Get()
	require.True(t, ok)

	params.Cursor.SetTo(cursor)
	second, err := h.ListEvents(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, second.Events, 2)
	assert.Equal(t, "two", second.Events[0].Details.Value["stack"])
	assert.Equal(t, "one", second.Events[1].Details.Value["stack"])
	assert.False(t, second.NextCursor.IsSet())

	unpaginated := generated.ListEventsParams{}
	unpaginated.Limit.SetTo(2)
	response, err := h.ListEvents(context.Background(), unpaginated)
	require.NoError(t, err)
	require.Len(t, response.Events, 2)
	assert.Equal(t, "three", response.Events[0].Details.Value["stack"])
	assert.Equal(t, "four", response.Events[1].Details.Value["stack"])
	assert.False(t, response.NextCursor.IsSet())
}

func TestHandlerListEventsRejectsInvalidCursor(t *testing.T) {
	t.Parallel()
	store := newHistoryStore(t)
	params := generated.ListEventsParams{}
	params.Sort.SetTo("time")
	params.Cursor.SetTo("not-a-cursor")
	_, err := (&handler{history: store}).ListEvents(context.Background(), params)
	require.Error(t, err)
}

func newHistoryStore(t *testing.T) *history.SQLStore {
	t.Helper()
	store, err := history.NewSQLStore(testutil.OpenSQLite(t), 50)
	require.NoError(t, err)
	return store
}

func storeEvent(store *history.SQLStore, ctx context.Context, payload events.Event) error {
	sequence := testPublicationSequence.Add(1)
	return storeEventEnvelope(store, ctx, payload, time.Unix(0, sequence).UTC(), sequence)
}

func storeEventAt(store *history.SQLStore, ctx context.Context, payload events.Event, at time.Time) error {
	sequence := testPublicationSequence.Add(1)
	return storeEventEnvelope(store, ctx, payload, at, sequence)
}

func storeEventEnvelope(
	store *history.SQLStore, ctx context.Context, payload events.Event, at time.Time, sequence int64,
) error {
	return store.Handle(ctx, events.Envelope{
		ID: fmt.Sprintf("test-event-%d", sequence), OccurredAt: at,
		PublicationSequence: sequence, Event: payload,
	})
}
