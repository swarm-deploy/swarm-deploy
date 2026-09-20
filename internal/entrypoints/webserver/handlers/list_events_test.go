package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestHandlerListEventsFiltersBySeverityAndCategory(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50, fs.NewLocalFileSystem())
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "abc"},
	}))
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

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50, fs.NewLocalFileSystem())
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

func TestHandlerListEventsFiltersBySwarmCategory(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50, fs.NewLocalFileSystem())
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.SyncManualStarted{}))
	require.NoError(
		t,
		store.Handle(context.Background(), &events.NodeDisconnected{
			NodeID:   "node-1",
			NodeName: "worker-1",
			Status:   "disconnected",
		}),
	)

	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Categories: []generated.EventCategory{generated.EventCategorySwarm},
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 1, "expected swarm events only")
	assert.Equal(t, "nodeDisconnected", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityAlert, resp.Events[0].Severity)
	assert.Equal(t, generated.EventCategorySwarm, resp.Events[0].Category)
}

func TestHandlerListEventsFiltersByType(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50, fs.NewLocalFileSystem())
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "abc"},
	}))
	require.NoError(t, store.Handle(context.Background(), &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "def"},
	}))
	require.NoError(t, store.Handle(context.Background(), &events.UserAuthenticated{Username: "alice"}))

	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Types: []string{string(events.TypeNameDeployFailed)},
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 1, "expected type-filtered response")
	assert.Equal(t, "deployFailed", resp.Events[0].Type)
	assert.Equal(t, generated.EventSeverityAlert, resp.Events[0].Severity)
}

func TestHandlerListEventsFiltersBySince(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "events.json")
	payload, err := json.Marshal([]history.Entry{
		{Type: events.TypeDeploySuccess, Severity: events.SeverityInfo, Category: events.CategorySync, CreatedAt: since.Add(-time.Second), Message: "old"},
		{Type: events.TypeDeployFailed, Severity: events.SeverityAlert, Category: events.CategorySync, CreatedAt: since, Message: "boundary"},
		{Type: events.TypeNodeDisconnected, Severity: events.SeverityAlert, Category: events.CategorySwarm, CreatedAt: since.Add(time.Second), Message: "new"},
	})
	require.NoError(t, err, "marshal history fixture")
	require.NoError(t, os.WriteFile(path, payload, 0o600), "write history fixture")

	store, err := history.NewStore(path, 50, fs.NewLocalFileSystem())
	require.NoError(t, err, "new history store")

	var generatedSince generated.OptDateTime
	generatedSince.SetTo(since)
	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Severities: []generated.EventSeverity{generated.EventSeverityAlert},
		Since:      generatedSince,
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 2, "expected alert events at or after since")
	assert.Equal(t, "deployFailed", resp.Events[0].Type)
	assert.Equal(t, "nodeDisconnected", resp.Events[1].Type)
}

func TestHandlerListEventsLimitsLatestFilteredEvents(t *testing.T) {
	t.Parallel()

	store, err := history.NewStore(filepath.Join(t.TempDir(), "events.json"), 50, fs.NewLocalFileSystem())
	require.NoError(t, err, "new history store")
	require.NoError(t, store.Handle(context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "abc"},
	}))
	require.NoError(t, store.Handle(context.Background(), &events.UserAuthenticated{Username: "alice"}))
	require.NoError(t, store.Handle(context.Background(), &events.DeployFailed{
		DeployEvent: events.DeployEvent{StackName: "api", Commit: "def"},
	}))
	require.NoError(t, store.Handle(context.Background(), &events.DeploySuccess{
		DeployEvent: events.DeployEvent{StackName: "worker", Commit: "ghi"},
	}))

	var limit generated.OptInt32
	limit.SetTo(2)
	h := &handler{history: store}
	resp, err := h.ListEvents(context.Background(), generated.ListEventsParams{
		Types: []string{string(events.TypeNameDeploySuccess), string(events.TypeNameDeployFailed)},
		Limit: limit,
	})
	require.NoError(t, err, "list events")
	require.Len(t, resp.Events, 2, "expected latest two deployment events")
	assert.Equal(t, "deployFailed", resp.Events[0].Type)
	assert.Equal(t, "deploySuccess", resp.Events[1].Type)
	assert.Equal(t, "ghi", resp.Events[1].Details.Value["commit"])
}
