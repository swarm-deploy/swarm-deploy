package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListServices(_ context.Context) (*generated.ServicesResponse, error) {
	items := []generated.ServiceInfo{}
	if h.services != nil {
		items = toGeneratedServiceInfos(h.services.List())
	}

	return &generated.ServicesResponse{
		Services: items,
	}, nil
}
