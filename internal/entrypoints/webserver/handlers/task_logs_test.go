package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerGetTaskLogsDefaults(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}

	entries := make(chan swarm.LogEntry)
	errs := make(chan error)
	close(entries)
	close(errs)

	serviceInspector.EXPECT().
		TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Follow: true, Limit: 200}).
		Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errs), nil)

	rec := httptest.NewRecorder()
	err := h.GetTaskLogs(context.Background(), generated.GetTaskLogsParams{
		TaskID: "task-1",
	}, rec)
	require.NoError(t, err)
	assert.Equal(t, "event: eof\ndata: {}\n\n", rec.Body.String())
}

func TestHandlerGetTaskLogsStreamsLogEventsAndEOF(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}

	entries := make(chan swarm.LogEntry, 2)
	errs := make(chan error)
	entries <- swarm.LogEntry{
		Timestamp: time.Date(2026, time.September, 15, 18, 20, 11, 123000000, time.UTC),
		Stream:    "stdout",
		Message:   "server started",
	}
	entries <- swarm.LogEntry{
		Timestamp: time.Date(2026, time.September, 15, 18, 20, 12, 456000000, time.UTC),
		Stream:    "stderr",
		Message:   "failed request",
	}
	close(entries)
	close(errs)

	serviceInspector.EXPECT().
		TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Follow: false, Limit: 50}).
		Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errs), nil)

	rec := httptest.NewRecorder()
	err := h.GetTaskLogs(context.Background(), generated.GetTaskLogsParams{
		TaskID: "task-1",
		Follow: generated.OptBool{
			Value: false,
			Set:   true,
		},
		Tail: generated.NewOptInt32(50),
	}, rec)
	require.NoError(t, err)

	assert.Contains(t, rec.Body.String(), "event: log\ndata: {\"timestamp\":\"2026-09-15T18:20:11.123Z\",\"stream\":\"stdout\",\"message\":\"server started\"}\n\n")
	assert.Contains(t, rec.Body.String(), "event: log\ndata: {\"timestamp\":\"2026-09-15T18:20:12.456Z\",\"stream\":\"stderr\",\"message\":\"failed request\"}\n\n")
	assert.Contains(t, rec.Body.String(), "event: eof\ndata: {}\n\n")
	assert.True(t, rec.Flushed, "expected direct handler to flush")
}

func TestHandlerGetTaskLogsStopsOnContextCancellationWithoutEOF(t *testing.T) {
	t.Parallel()

	entries := make(chan swarm.LogEntry)
	errs := make(chan error)
	defer close(entries)
	defer close(errs)

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}

	serviceInspector.EXPECT().
		TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Follow: true, Limit: 200}).
		Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errs), nil)

	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	done := make(chan error)

	go func() {
		done <- h.GetTaskLogs(ctx, generated.GetTaskLogsParams{TaskID: "task-1"}, rec)
	}()
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "expected handler to return after request cancellation")
	}

	assert.NotContains(t, rec.Body.String(), "event: eof")
}

func TestGetTaskLogsOgenServerStreamsSSE(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	apiHandler, err := generated.NewServer(
		h,
		h,
		generated.WithErrorHandler(HandleHTTPError),
	)
	require.NoError(t, err)

	entries := make(chan swarm.LogEntry, 1)
	errs := make(chan error)
	entries <- swarm.LogEntry{
		Timestamp: time.Date(2026, time.September, 15, 18, 20, 11, 123000000, time.UTC),
		Stream:    "stdout",
		Message:   "server started",
	}
	close(entries)
	close(errs)

	serviceInspector.EXPECT().
		TaskLogs(gomock.Any(), "task-1", swarm.TaskLogsOptions{Follow: false, Limit: 50}).
		Return((<-chan swarm.LogEntry)(entries), (<-chan error)(errs), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-1/logs?follow=false&tail=50", nil)

	apiHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Body.String(), "event: log")
	assert.Contains(t, rec.Body.String(), "event: eof\ndata: {}\n\n")
	assert.True(t, rec.Flushed, "expected generated SSE encoder to flush")
}

func TestGetTaskLogsOgenServerRejectsInvalidQuery(t *testing.T) {
	t.Parallel()

	apiHandler, err := generated.NewServer(
		&handler{},
		&handler{},
		generated.WithErrorHandler(HandleHTTPError),
	)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task-1/logs?tail=0", nil)

	apiHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
