package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	dockernetwork "github.com/docker/docker/api/types/network"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// GetNetwork returns detailed runtime metadata for one Docker network.
func (h *handler) GetNetwork(
	ctx context.Context,
	params generated.GetNetworkParams,
) (generated.GetNetworkRes, error) {
	network, err := h.networks.Get(ctx, params.NetworkID, dockernetwork.InspectOptions{Verbose: true, Scope: "swarm"})
	if errors.Is(err, swarm.ErrNetworkNotFound) {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("%w: %q", swarm.ErrNetworkNotFound, params.NetworkID))
	}
	if err != nil {
		return nil, fmt.Errorf("get docker network: %w", err)
	}

	mapped := toGeneratedNetwork(network)

	return &mapped, nil
}
