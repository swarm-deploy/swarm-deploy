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
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultTaskLogsTail = 200

type taskLogSSEEvent struct {
	Timestamp  string          `json:"timestamp"`
	Stream     string          `json:"stream"`
	Level      logparser.Level `json:"level,omitempty"`
	Message    string          `json:"message"`
	Attributes map[string]any  `json:"attributes,omitempty"`
	RawMessage string          `json:"raw_message,omitempty"`
}

type taskLogCountsSSEEvent struct {
	Counts map[logparser.Level]int `json:"counts"`
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
	query, err := parseTaskLogsQuery(params)
	if err != nil {
		return withStatusError(http.StatusBadRequest, err)
	}

	entries, errs, err := h.taskLogs.Stream(ctx, params.TaskID, query)
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

	return streamTaskLogEvents(ctx, w, flusher, entries, errs, query.Parse)
}

func parseTaskLogsQuery(params generated.GetTaskLogsParams) (logparser.Query, error) {
	parse := params.Parse.Or(false)
	levels, err := logparser.ParseLevels(params.Levels)
	if err != nil {
		return logparser.Query{}, err
	}
	if !parse && len(levels) > 0 {
		return logparser.Query{}, errors.New("level filters require parse=true")
	}

	since := optionalTaskLogsTime(params.Since)
	until := optionalTaskLogsTime(params.Until)
	if since != nil && until != nil && since.After(*until) {
		return logparser.Query{}, errors.New("since must be before or equal to until")
	}

	return logparser.Query{
		Follow: params.Follow.Or(true),
		Limit:  int(params.Tail.Or(defaultTaskLogsTail)),
		Since:  since,
		Until:  until,
		Parse:  parse,
		Levels: levels,
	}, nil
}

func streamTaskLogEvents(
	ctx context.Context,
	w io.Writer,
	flusher sseFlusher,
	entries <-chan logparser.StreamItem,
	errs <-chan error,
	parse bool,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case item, ok := <-entries:
			if !ok {
				return finishTaskLogStream(w, flusher, errs)
			}
			if err := writeTaskLogStreamItem(w, flusher, item, parse); err != nil {
				return err
			}
		}
	}
}

func finishTaskLogStream(w io.Writer, flusher sseFlusher, errs <-chan error) error {
	if streamErr := <-errs; streamErr != nil {
		return streamErr
	}

	writeTaskLogsEOFEvent(w, flusher)
	return nil
}

func writeTaskLogStreamItem(w io.Writer, flusher sseFlusher, item logparser.StreamItem, parsed bool) error {
	if parsed {
		if err := writeTaskLogCountsEvent(w, flusher, item.Counts); err != nil {
			return err
		}
	}
	if !item.Included {
		return nil
	}

	return writeTaskLogEvent(w, flusher, item.Entry, parsed)
}

func writeTaskLogEvent(w io.Writer, flusher sseFlusher, entry logparser.Entry, parsed bool) error {
	timestamp := ""
	if !entry.Timestamp.IsZero() {
		timestamp = entry.Timestamp.UTC().Format(time.RFC3339Nano)
	}

	event := taskLogSSEEvent{
		Timestamp: timestamp,
		Stream:    entry.Stream,
		Message:   entry.Message,
	}
	if parsed {
		event.Level = entry.Level
		event.Attributes = entry.Attributes
		event.RawMessage = entry.RawMessage
	}

	payload, err := json.Marshal(event)
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

func writeTaskLogCountsEvent(w io.Writer, flusher sseFlusher, counts map[logparser.Level]int) error {
	payload, err := json.Marshal(taskLogCountsSSEEvent{Counts: counts})
	if err != nil {
		return fmt.Errorf("marshal log level counts: %w", err)
	}

	if _, err = fmt.Fprintf(w, "event: counts\ndata: %s\n\n", payload); err != nil {
		return fmt.Errorf("write log level counts: %w", err)
	}
	if err = flusher.Flush(); err != nil {
		return fmt.Errorf("flush log level counts: %w", err)
	}

	return nil
}

func optionalTaskLogsTime(value generated.OptDateTime) *time.Time {
	parsed, ok := value.Get()
	if !ok {
		return nil
	}
	parsed = parsed.UTC()

	return &parsed
}

func writeTaskLogsEOFEvent(w io.Writer, flusher sseFlusher) {
	_, _ = fmt.Fprint(w, "event: eof\ndata: {}\n\n")
	_ = flusher.Flush()
}
