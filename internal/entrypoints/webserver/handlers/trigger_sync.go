package handlers

import (
	"context"

	"github.com/artarts36/swarm-deploy/internal/controller"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) TriggerSync(_ context.Context) (*generated.QueueResponse, error) {
	return &generated.QueueResponse{
		Queued: h.control.Trigger(controller.TriggerManual),
	}, nil
}
