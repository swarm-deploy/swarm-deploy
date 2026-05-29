package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) TriggerSync(ctx context.Context) (*generated.QueueResponse, error) {
	return &generated.QueueResponse{
		Queued: h.control.Manual(ctx),
	}, nil
}
