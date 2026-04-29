package swarm

import (
	"context"
	"fmt"
	"sort"

	"github.com/docker/docker/api/types/filters"
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
	networks, err := m.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{
		Filters: filters.NewArgs(filters.Arg("scope", "swarm")),
	})
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
		if isNotFoundErr(err) {
			return Network{}, ErrNetworkNotFound
		}

		return Network{}, fmt.Errorf("inspect network: %w", err)
	}

	return m.mapNetwork(network), nil
}

func (m *NetworkManager) Create(ctx context.Context, req CreateNetworkRequest) (string, error) {
	resp, err := m.dockerClient.NetworkCreate(ctx, req.Name, dockernetwork.CreateOptions{
		Driver:     req.Driver,
		Attachable: req.Attachable,
		Internal:   req.Internal,
		Options:    req.Options,
		Labels:     req.Labels,
	})
	if err != nil {
		return "", err
	}

	return resp.ID, nil
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
		Options:    network.Options,
	}
}

func (*NetworkManager) sortNetworks(networks []Network) {
	sort.Slice(networks, func(i, j int) bool {
		return networks[i].Name < networks[j].Name
	})
}
