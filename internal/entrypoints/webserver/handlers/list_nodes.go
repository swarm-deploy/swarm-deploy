package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListNodes(ctx context.Context) (*generated.NodesResponse, error) {
	items := []generated.NodeInfo{}
	if h.nodes != nil {
		nodes, err := h.nodes.ReadAll(ctx)
		if err != nil {
			return nil, err
		}
		items = toGeneratedNodes(nodes)
	}

	return &generated.NodesResponse{
		Nodes: items,
	}, nil
}
