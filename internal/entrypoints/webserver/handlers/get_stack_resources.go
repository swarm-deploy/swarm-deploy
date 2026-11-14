package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func (h *handler) GetStackResources(
	ctx context.Context,
	params generated.GetStackResourcesParams,
) (*generated.StackResourcesResponse, error) {
	if _, found := h.resolveStackComposeFile(params.Stack); !found {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("stack %s not found", params.Stack))
	}

	networks, err := h.networks.List(ctx, swarm.ListNetworksFilter{Stacks: []string{params.Stack}})
	if err != nil {
		return nil, fmt.Errorf("list stack networks: %w", err)
	}
	secrets, err := h.dockerSecrets.List(ctx, swarm.ListSecretsFilter{StackName: params.Stack})
	if err != nil {
		return nil, fmt.Errorf("list stack secrets: %w", err)
	}
	configs, err := h.dockerConfigs.List(ctx, swarm.ListConfigsFilter{StackName: params.Stack})
	if err != nil {
		return nil, fmt.Errorf("list stack configs: %w", err)
	}

	ownedNetworks := make([]swarm.Network, 0, len(networks))
	for _, network := range networks {
		if labelsdict.GetStackName(network.Labels) == params.Stack {
			network.Labels = cloneStringMap(network.Labels)
			ownedNetworks = append(ownedNetworks, network)
		}
	}
	result := &generated.StackResourcesResponse{
		Networks: toGeneratedNetworks(ownedNetworks),
		Secrets:  make([]generated.StackResourceInfo, 0, len(secrets)),
		Configs:  make([]generated.StackResourceInfo, 0, len(configs)),
	}
	for _, secret := range secrets {
		if labelsdict.GetStackName(secret.Labels) == params.Stack {
			result.Secrets = append(result.Secrets, generated.StackResourceInfo{ID: secret.ID, Name: secret.Name})
		}
	}
	for _, cfg := range configs {
		if labelsdict.GetStackName(cfg.Labels) == params.Stack {
			result.Configs = append(result.Configs, generated.StackResourceInfo{ID: cfg.ID, Name: cfg.Name})
		}
	}
	sortStackResourceInfo(result.Secrets)
	sortStackResourceInfo(result.Configs)
	return result, nil
}

func sortStackResourceInfo(resources []generated.StackResourceInfo) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Name != resources[j].Name {
			return resources[i].Name < resources[j].Name
		}
		return resources[i].ID < resources[j].ID
	})
}
