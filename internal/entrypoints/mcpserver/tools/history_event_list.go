package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
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
				"severities": map[string]any{
					"type":        "array",
					"description": "Optional event severities filter.",
					"items": map[string]any{
						"type": "string",
						"enum": []string{
							string(events.SeverityInfo),
							string(events.SeverityWarn),
							string(events.SeverityError),
							string(events.SeverityAlert),
						},
					},
				},
				"categories": map[string]any{
					"type":        "array",
					"description": "Optional event categories filter.",
					"items": map[string]any{
						"type": "string",
						"enum": []string{
							string(events.CategorySync),
							string(events.CategorySecurity),
						},
					},
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
	severities, err := parseHistorySeverities(request.Payload["severities"])
	if err != nil {
		return routing.Response{}, err
	}
	categories, err := parseHistoryCategories(request.Payload["categories"])
	if err != nil {
		return routing.Response{}, err
	}

	entries := history.FilterEntries(l.history.List(), severities, categories)
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

func parseHistorySeverities(raw any) ([]events.Severity, error) {
	values, err := parseStringList(raw, "severities")
	if err != nil {
		return nil, err
	}

	out := make([]events.Severity, 0, len(values))
	for _, value := range values {
		parsed, ok := events.ParseSeverity(value)
		if !ok {
			return nil, fmt.Errorf("severities contains unknown value %q", value)
		}
		out = append(out, parsed)
	}

	return out, nil
}

func parseHistoryCategories(raw any) ([]events.Category, error) {
	values, err := parseStringList(raw, "categories")
	if err != nil {
		return nil, err
	}

	out := make([]events.Category, 0, len(values))
	for _, value := range values {
		parsed, ok := events.ParseCategory(value)
		if !ok {
			return nil, fmt.Errorf("categories contains unknown value %q", value)
		}
		out = append(out, parsed)
	}

	return out, nil
}

func parseStringList(raw any, field string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	switch value := raw.(type) {
	case []string:
		return value, nil
	case string:
		return []string{value}, nil
	default:
		return nil, fmt.Errorf("%s must be array of strings", field)
	}
}
