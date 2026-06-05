package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	resourcegraph "github.com/swarm-deploy/swarm-deploy/internal/resources/graph"
)

func (h *handler) GetGraph(_ context.Context) (*generated.GraphResponse, error) {
	built := resourcegraph.NewBuilder().Build(h.services.List())

	return toGeneratedGraph(built), nil
}
