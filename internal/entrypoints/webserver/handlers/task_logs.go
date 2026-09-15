package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultTaskLogsTail = 200

type taskLogSSEEvent struct {
	Timestamp string `json:"timestamp"`
	Stream    string `json:"stream"`
	Message   string `json:"message"`
}

type sseFlusher interface {
	Flush() error
}

// GetTaskLogs streams Docker Swarm task logs as Server-Sent Events.
func (h *handler) GetTaskLogs(
	ctx context.Context,
	params generated.GetTaskLogsParams,
	w http.ResponseWriter,
) error {
	tail := int(params.Tail.Or(defaultTaskLogsTail))

	entries, errs, err := h.serviceInspector.TaskLogs(ctx, params.TaskID, swarm.TaskLogsOptions{
		Follow: params.Follow.Or(true),
		Limit:  tail,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, swarm.ErrServiceNotFound) {
			status = http.StatusNotFound
		}

		return withStatusError(status, err)
	}

	flusher := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err = flusher.Flush(); err != nil {
		return fmt.Errorf("flush log stream headers: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case entry, ok := <-entries:
			if !ok {
				if streamErr := <-errs; streamErr != nil {
					return streamErr
				}

				writeTaskLogsEOFEvent(w, flusher)
				return nil
			}

			if err = writeTaskLogEvent(w, flusher, entry); err != nil {
				return err
			}
		}
	}
}

func writeTaskLogEvent(w io.Writer, flusher sseFlusher, entry swarm.LogEntry) error {
	timestamp := ""
	if !entry.Timestamp.IsZero() {
		timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
	}

	payload, err := json.Marshal(taskLogSSEEvent{
		Timestamp: timestamp,
		Stream:    entry.Stream,
		Message:   entry.Message,
	})
	if err != nil {
		return fmt.Errorf("marshal log event: %w", err)
	}

	if _, err = fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload); err != nil {
		return fmt.Errorf("write log event: %w", err)
	}
	if err = flusher.Flush(); err != nil {
		return fmt.Errorf("flush log event: %w", err)
	}

	return nil
}

func writeTaskLogsEOFEvent(w io.Writer, flusher sseFlusher) {
	_, _ = fmt.Fprint(w, "event: eof\ndata: {}\n\n")
	_ = flusher.Flush()
}
