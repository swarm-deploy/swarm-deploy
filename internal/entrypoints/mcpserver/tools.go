package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/assistant"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/event/history"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 200
)

// Tools provides direct-call MCP tools without running external server.
type Tools struct {
	history historyReader
	control syncTrigger
}

var _ assistant.ToolExecutor = (*Tools)(nil)

type historyReader interface {
	// List returns current event history snapshot.
	List() []history.Entry
}

type syncTrigger interface {
	// Trigger enqueues synchronization by reason.
	Trigger(reason controller.TriggerReason) bool
}

// NewTools creates a tool executor.
func NewTools(historyStore historyReader, control syncTrigger) *Tools {
	return &Tools{
		history: historyStore,
		control: control,
	}
}

// Definitions returns available MCP tool metadata.
func (t *Tools) Definitions() []assistant.ToolDefinition {
	return []assistant.ToolDefinition{
		{
			Name:        "list_history_events",
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
		},
		{
			Name:        "sync",
			Description: "Triggers manual synchronization run.",
			ParametersJSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// Execute runs a tool by name.
func (t *Tools) Execute(_ context.Context, name string, arguments map[string]any) (string, error) {
	switch name {
	case "list_history_events":
		return t.executeListHistoryEvents(arguments)
	case "sync":
		return t.executeSync()
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (t *Tools) executeListHistoryEvents(arguments map[string]any) (string, error) {
	if t.history == nil {
		return "", fmt.Errorf("event history store is not configured")
	}

	limit, err := parseHistoryLimit(arguments["limit"])
	if err != nil {
		return "", err
	}

	entries := t.history.List()
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	payload := struct {
		Events []history.Entry `json:"events"`
	}{
		Events: entries,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode history tool response: %w", err)
	}

	return string(encoded), nil
}

func (t *Tools) executeSync() (string, error) {
	if t.control == nil {
		return "", fmt.Errorf("controller is not configured")
	}

	queued := t.control.Trigger(controller.TriggerManual)
	payload := struct {
		Queued bool `json:"queued"`
	}{
		Queued: queued,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode sync tool response: %w", err)
	}

	return string(encoded), nil
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
