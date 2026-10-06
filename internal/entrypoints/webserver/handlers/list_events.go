package handlers

import (
	"context"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
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

	types := make([]events.TypeName, 0, len(params.Types))
	for _, eventType := range params.Types {
		types = append(types, events.TypeName(eventType))
	}

	var since *time.Time
	if value, ok := params.Since.Get(); ok {
		since = &value
	}

	entries := h.history.List()
	entries = history.FilterEntries(entries, severities, categories, types, since)
	if value, ok := params.Limit.Get(); ok {
		entries = limitLatestEntries(entries, int(value))
	}
	items := toGeneratedEvents(entries)

	return &generated.EventHistoryResponse{
		Events: items,
	}, nil
}

func limitLatestEntries(entries []history.Entry, limit int) []history.Entry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}

	return entries[len(entries)-limit:]
}
