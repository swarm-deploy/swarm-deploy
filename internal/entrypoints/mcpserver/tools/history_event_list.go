package tools

import (
	"context"
	"fmt"

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

type listHistoryEventsRequest struct {
	Limit      *int     `json:"limit"`
	Severities []string `json:"severities"`
	Categories []string `json:"categories"`
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
		Request: listHistoryEventsRequest{},
	}
}

// Execute runs history_event_list tool.
func (l *ListHistoryEvents) Execute(_ context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[listHistoryEventsRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	limit, err := parseHistoryLimit(parsedRequest.Limit)
	if err != nil {
		return routing.Response{}, err
	}
	severities, err := parseHistorySeverities(parsedRequest.Severities)
	if err != nil {
		return routing.Response{}, err
	}
	categories, err := parseHistoryCategories(parsedRequest.Categories)
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

func parseHistoryLimit(limit *int) (int, error) {
	if limit == nil {
		return defaultHistoryLimit, nil
	}

	parsed := *limit
	if parsed <= 0 {
		return 0, fmt.Errorf("limit must be > 0")
	}
	if parsed > maxHistoryLimit {
		parsed = maxHistoryLimit
	}

	return parsed, nil
}

func parseHistorySeverities(values []string) ([]events.Severity, error) {
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

func parseHistoryCategories(values []string) ([]events.Category, error) {
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
