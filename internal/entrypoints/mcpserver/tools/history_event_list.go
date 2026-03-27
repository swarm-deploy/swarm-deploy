package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/event/history"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 200
)

// ListHistoryEvents returns latest events from local event history.
type ListHistoryEvents struct {
	history HistoryReader
}

// NewListHistoryEvents creates history_event_list component.
func NewListHistoryEvents(historyStore HistoryReader) *ListHistoryEvents {
	return &ListHistoryEvents{history: historyStore}
}

// Definition returns tool metadata visible to the model.
func (l *ListHistoryEvents) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "history_event_list",
		Description: "Returns latest events from local event history.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     maxHistoryLimit,
					"description": "Maximum number of latest events to return.",
				},
			},
		},
	}
}

// Execute runs history_event_list tool.
func (l *ListHistoryEvents) Execute(_ context.Context, request routing.Request) (routing.Response, error) {
	limit, err := parseHistoryLimit(request.Payload["limit"])
	if err != nil {
		return routing.Response{}, err
	}

	entries := l.history.List()
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	payload := struct {
		Events []history.Entry `json:"events"`
	}{
		Events: entries,
	}
	return routing.Response{
		Payload: payload,
	}, nil
}

func parseHistoryLimit(raw any) (int, error) {
	if raw == nil {
		return defaultHistoryLimit, nil
	}

	var parsed int
	switch value := raw.(type) {
	case float64:
		if value != math.Trunc(value) {
			return 0, fmt.Errorf("limit must be integer")
		}
		parsed = int(value)
	case int:
		parsed = value
	case int64:
		parsed = int(value)
	case json.Number:
		number, err := strconv.Atoi(value.String())
		if err != nil {
			return 0, fmt.Errorf("limit must be integer: %w", err)
		}
		parsed = number
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("limit must be integer: %w", err)
		}
		parsed = number
	default:
		return 0, fmt.Errorf("limit must be integer")
	}

	if parsed <= 0 {
		return 0, fmt.Errorf("limit must be > 0")
	}
	if parsed > maxHistoryLimit {
		parsed = maxHistoryLimit
	}

	return parsed, nil
}
