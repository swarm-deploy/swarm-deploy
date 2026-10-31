package swarm

import (
	"context"
	"fmt"
	"sort"

	"github.com/docker/docker/api/types/filters"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// NetworkManager reads current Docker networks snapshot.
type networkManager struct {
	dockerClient *client.Client
}

func newNetworkManager(dockerClient *client.Client) NetworkManager {
	return &networkManager{
		dockerClient: dockerClient,
	}
}

// List returns Docker networks matching the provided filter.
func (m *networkManager) List(ctx context.Context, filter ListNetworksFilter) ([]Network, error) {
	networks, err := m.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{
		Filters: buildNetworkListFilters(filter),
	})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	mapped := make([]Network, 0, len(networks))
	for _, network := range networks {
		mappedNetwork := m.mapNetwork(network)
		if matchesNetworkListFilter(mappedNetwork, filter) {
			mapped = append(mapped, mappedNetwork)
		}
	}
	m.sortNetworks(mapped)

	return mapped, nil
}

func buildNetworkListFilters(filter ListNetworksFilter) filters.Args {
	filterArgs := make([]filters.KeyValuePair, 0, len(filter.Stacks)+1)
	filterArgs = append(filterArgs, filters.Arg("scope", "swarm"))
	if len(filter.Stacks) == 1 {
		filterArgs = append(filterArgs, filters.Arg("label", stackNamespaceLabelKey+"="+filter.Stacks[0]))
	}

	return filters.NewArgs(filterArgs...)
}

func matchesNetworkListFilter(network Network, filter ListNetworksFilter) bool {
	if len(filter.Stacks) == 0 {
		return true
	}
	for _, stack := range filter.Stacks {
		if network.Stack == stack {
			return true
		}
	}

	return false
}

// Map returns current Docker networks snapshot.
func (m *networkManager) Map(ctx context.Context, ids []string) (map[string]Network, error) {
	filterArgs := make([]filters.KeyValuePair, len(ids))
	for i, id := range ids {
		filterArgs[i] = filters.Arg("id", id)
	}

	networks, err := m.dockerClient.NetworkList(ctx, dockernetwork.ListOptions{
		Filters: filters.NewArgs(filterArgs...),
	})
	if err != nil {
		return nil, fmt.Errorf("list docker networks: %w", err)
	}

	mapped := make(map[string]Network, len(networks))
	for _, network := range networks {
		mapped[network.ID] = m.mapNetwork(network)
	}

	return mapped, nil
}

func (m *networkManager) Get(ctx context.Context, name string) (Network, error) {
	network, err := m.dockerClient.NetworkInspect(ctx, name, dockernetwork.InspectOptions{})
	if err != nil {
		if isNotFoundErr(err) {
			return Network{}, ErrNetworkNotFound
		}

		return Network{}, fmt.Errorf("inspect network: %w", err)
	}

	return m.mapNetwork(network), nil
}

func (m *networkManager) Create(ctx context.Context, req CreateNetworkRequest) (string, error) {
	resp, err := m.dockerClient.NetworkCreate(ctx, req.Name, dockernetwork.CreateOptions{
		Driver:     req.Driver,
		IPAM:       req.IPAM,
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

func (*networkManager) mapNetwork(network dockernetwork.Summary) Network {
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
		Stack:      labelsdict.GetStackName(network.Labels),
	}
}

func (*networkManager) sortNetworks(networks []Network) {
	sort.Slice(networks, func(i, j int) bool {
		return networks[i].Name < networks[j].Name
	})
}
