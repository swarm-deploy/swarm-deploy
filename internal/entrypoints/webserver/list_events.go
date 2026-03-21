package webserver

import (
	"context"

	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListEvents(_ context.Context) (*generated.EventHistoryResponse, error) {
	items := []generated.EventHistoryItem{}
	if h.history != nil {
		items = toGeneratedEvents(h.history.List())
	}

	return &generated.EventHistoryResponse{
		Events: items,
	}, nil
}
