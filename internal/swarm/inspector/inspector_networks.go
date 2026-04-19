package inspector

import (
	"context"
	"fmt"

	dockernetwork "github.com/docker/docker/api/types/network"
)

// InspectNetworks returns current Docker networks snapshot.
func (i *Inspector) InspectNetworks(ctx context.Context) ([]NetworkInfo, error) {
	networks, err := i.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	mapped := make([]NetworkInfo, len(networks))
	for i, network := range networks {
		mapped[i] = toNetworkInfo(network)
	}
	sortNetworkInfos(mapped)

	return mapped, nil
}

func toNetworkInfo(network dockernetwork.Summary) NetworkInfo {
	return NetworkInfo{
		Name:       network.Name,
		Scope:      network.Scope,
		Driver:     network.Driver,
		Internal:   network.Internal,
		Attachable: network.Attachable,
		Ingress:    network.Ingress,
		Labels:     network.Labels,
	}
}
