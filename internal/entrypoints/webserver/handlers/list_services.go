package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

func (h *handler) ListServices(ctx context.Context) (*generated.ServicesResponse, error) {
	items := []generated.ServiceInfo{}
	if h.services != nil {
		snapshot := model.Runtime{}
		if h.stateStore != nil {
			var err error
			snapshot, err = h.stateStore.Read(ctx)
			if err != nil {
				return nil, err
			}
		}

		services, err := h.services.ReadAll(ctx)
		if err != nil {
			return nil, err
		}
		items = toGeneratedServiceInfos(services, snapshot)
	}

	return &generated.ServicesResponse{
		Services: items,
	}, nil
}
