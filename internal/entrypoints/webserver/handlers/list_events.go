package handlers

import (
	"context"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/history"
)

const defaultEventPageSize int32 = 50

func (h *handler) ListEvents(
	ctx context.Context,
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

	// Preserve the legacy response order for callers which do not opt into
	// pagination (notably the Overview latest-deployments widget).
	if !params.Sort.IsSet() && !params.Order.IsSet() && !params.Cursor.IsSet() {
		entries, err := h.history.ReadRecent(ctx, history.QueryOptions{
			Severities: severities, Categories: categories, Types: types, Since: since,
			Limit: int(params.Limit.Or(0)),
		})
		if err != nil {
			return nil, err
		}
		return &generated.EventHistoryResponse{Events: toGeneratedEvents(entries)}, nil
	}

	page, err := h.history.QueryPage(ctx, history.QueryOptions{
		Severities: severities,
		Categories: categories,
		Types:      types,
		Since:      since,
		Limit:      int(params.Limit.Or(defaultEventPageSize)),
		Cursor:     params.Cursor.Or(""),
		Sort:       history.SortOrder(params.Sort.Or("time") + "_" + params.Order.Or("desc")),
	})
	if err != nil {
		return nil, withStatusError(http.StatusBadRequest, err)
	}
	response := &generated.EventHistoryResponse{Events: toGeneratedEvents(page.Entries)}
	if page.NextCursor != "" {
		response.NextCursor.SetTo(page.NextCursor)
	}
	return response, nil
}
