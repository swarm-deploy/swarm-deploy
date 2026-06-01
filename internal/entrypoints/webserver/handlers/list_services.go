package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
)

func (h *handler) ListServices(_ context.Context) (*generated.ServicesResponse, error) {
	items := []generated.ServiceInfo{}
	if h.services != nil {
		snapshot := model.Runtime{}
		if h.stateStore != nil {
			snapshot = h.stateStore.Get()
		}

		items = toGeneratedServiceInfos(h.services.List(), snapshot)
	}

	return &generated.ServicesResponse{
		Services: items,
	}, nil
}
