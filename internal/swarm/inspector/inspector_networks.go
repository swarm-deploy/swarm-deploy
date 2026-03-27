package inspector

import (
	"context"
	"errors"
	"fmt"

	dockernetwork "github.com/docker/docker/api/types/network"
)

// InspectNetworks returns current Docker networks snapshot.
func (i *Inspector) InspectNetworks(ctx context.Context) ([]NetworkInfo, error) {
	if i.dockerClient == nil {
		return nil, errors.New("docker api client is not initialized")
	}

	networks, err := i.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	mapped := make([]NetworkInfo, 0, len(networks))
	for _, network := range networks {
		mapped = append(mapped, toNetworkInfo(network))
	}
	sortNetworkInfos(mapped)

	return mapped, nil
}

func toNetworkInfo(network dockernetwork.Summary) NetworkInfo {
	return normalizeNetworkInfo(NetworkInfo{
		Name:       network.Name,
		Scope:      network.Scope,
		Driver:     network.Driver,
		Internal:   network.Internal,
		Attachable: network.Attachable,
		Ingress:    network.Ingress,
		Labels:     cloneStringMap(network.Labels),
	})
}
