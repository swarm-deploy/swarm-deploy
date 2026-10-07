package handlers

import (
	"context"
	"fmt"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListNetworks(ctx context.Context) (*generated.NetworksResponse, error) {
	networks, err := h.networks.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	return &generated.NetworksResponse{
		Networks: toGeneratedNetworks(networks),
	}, nil
}
