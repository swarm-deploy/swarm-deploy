package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListNodes(_ context.Context) (*generated.NodesResponse, error) {
	items := []generated.NodeInfo{}
	if h.nodes != nil {
		items = toGeneratedNodes(h.nodes.List())
	}

	return &generated.NodesResponse{
		Nodes: items,
	}, nil
}
