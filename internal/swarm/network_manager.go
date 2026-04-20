package swarm

import (
	"context"
	"fmt"
	"sort"

	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// NetworkManager reads current Docker networks snapshot.
type NetworkManager struct {
	dockerClient *client.Client
}

func newNetworkManager(dockerClient *client.Client) *NetworkManager {
	return &NetworkManager{
		dockerClient: dockerClient,
	}
}

// List returns current Docker networks snapshot.
func (m *NetworkManager) List(ctx context.Context) ([]Network, error) {
	networks, err := m.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	mapped := make([]Network, len(networks))
	for i, network := range networks {
		mapped[i] = m.mapNetwork(network)
	}
	m.sortNetworks(mapped)

	return mapped, nil
}

func (m *NetworkManager) Get(ctx context.Context, name string) (Network, error) {
	network, err := m.dockerClient.NetworkInspect(ctx, name, dockernetwork.InspectOptions{})
	if err != nil {
		return Network{}, fmt.Errorf("inspect network: %w", err)
	}

	return m.mapNetwork(network), nil
}

func (*NetworkManager) mapNetwork(network dockernetwork.Summary) Network {
	return Network{
		ID:         network.ID,
		Name:       network.Name,
		Scope:      network.Scope,
		Driver:     network.Driver,
		Internal:   network.Internal,
		Attachable: network.Attachable,
		Ingress:    network.Ingress,
		Labels:     network.Labels,
	}
}

func (*NetworkManager) sortNetworks(networks []Network) {
	sort.Slice(networks, func(i, j int) bool {
		return networks[i].Name < networks[j].Name
	})
}
