package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
)

func TestHandlerListEventsFiltersBySeverityAndCategory(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50)
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.DeploySuccess{StackName: "api", Commit: "abc"}))
	require.NoError(t, store.Handle(context.Background(), &events.UserAuthenticated{Username: "alice"}))
	require.NoError(
		t,
		store.Handle(context.Background(), &events.SendNotificationFailed{
			EventType:   events.TypeDeploySuccess,
			Destination: "telegram",
			Channel:     "ops",
			Error:       errors.New("timeout"),
		}),
	)

	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityError},
		Categories: []generated.EventCategory{generated.EventCategorySync},
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 1, "expected filtered response")
	assert.Equal(t, "sendNotificationFailed", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityError, resp.Events[0].Severity)
	assert.Equal(t, generated.EventCategorySync, resp.Events[0].Category)
}

func TestHandlerListEventsUsesOrWithinSeverityFilter(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50)
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.SyncManualStarted{}))
	require.NoError(
		t,
		store.Handle(context.Background(), &events.SendNotificationFailed{
			EventType:   events.TypeDeployFailed,
			Destination: "custom",
			Channel:     "audit",
			Error:       errors.New("down"),
		}),
	)
	require.NoError(t, store.Handle(context.Background(), &events.AssistantPromptInjectionDetected{}))

	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityInfo, generated.EventSeverityError},
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 2, "expected info+error events only")
	assert.Equal(t, "syncManualStarted", resp.Events[0].Type)
	assert.Equal(t, "sendNotificationFailed", resp.Events[1].Type)
}
