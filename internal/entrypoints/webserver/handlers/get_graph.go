package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	resourcegraph "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/graph"
)

func (h *handler) GetGraph(ctx context.Context) (*generated.GraphResponse, error) {
	services, err := h.services.ReadAll(ctx)
	if err != nil {
		return nil, err
	}
	built := resourcegraph.NewBuilder().Build(services)

	return toGeneratedGraph(built), nil
}
