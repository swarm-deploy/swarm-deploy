package handlers

import (
	"context"
	"fmt"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (h *handler) ListNetworks(
	ctx context.Context,
	params generated.ListNetworksParams,
) (*generated.NetworksResponse, error) {
	networks, err := h.networks.List(ctx, swarm.ListNetworksFilter{Stacks: params.Stack})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	return &generated.NetworksResponse{
		Networks: toGeneratedNetworks(networks),
	}, nil
}
