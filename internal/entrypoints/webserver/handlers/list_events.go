package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
)

func (h *handler) ListEvents(
	_ context.Context,
	params generated.ListEventsParams,
) (*generated.EventHistoryResponse, error) {
	severities := make([]events.Severity, 0, len(params.Severities))
	for _, severity := range params.Severities {
		parsed, ok := events.ParseSeverity(string(severity))
		if !ok {
			continue
		}
		severities = append(severities, parsed)
	}

	categories := make([]events.Category, 0, len(params.Categories))
	for _, category := range params.Categories {
		parsed, ok := events.ParseCategory(string(category))
		if !ok {
			continue
		}
		categories = append(categories, parsed)
	}

	entries := h.history.List()
	entries = history.FilterEntries(entries, severities, categories)
	items := toGeneratedEvents(entries)

	return &generated.EventHistoryResponse{
		Events: items,
	}, nil
}
