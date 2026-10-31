package converter

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const defaultNetwork = "default"

func networkDefinitions(desired compose.Compose) map[string]compose.Network {
	definitions := make(map[string]compose.Network, len(desired.Networks)+1)
	maps.Copy(definitions, desired.Networks)
	for _, service := range desired.Services {
		if needsDefaultNetwork(service) {
			if _, exists := definitions[defaultNetwork]; !exists {
				definitions[defaultNetwork] = compose.Network{}
			}
		}
	}
	return definitions
}

func needsDefaultNetwork(service compose.Service) bool {
	if service.Networks == nil || len(service.Networks.List) == 0 {
		return true
	}
	for _, network := range service.Networks.List {
		if network != nil && network.Alias == defaultNetwork {
			return true
		}
	}
	for _, job := range service.InitJobs {
		if job.Networks == nil {
			continue
		}
		for _, network := range job.Networks.List {
			if network != nil && network.Alias == defaultNetwork {
				return true
			}
		}
	}
	return false
}

func (c *Converter) convertNetworks(
	ctx context.Context, stackName string, desired compose.Compose,
) ([]NetworkSpec, error) {
	definitions := networkDefinitions(desired)
	result := make([]NetworkSpec, 0, len(definitions))
	byName := map[string]swarm.Network{}
	for _, alias := range sortedKeys(definitions) {
		definition := definitions[alias]
		name := resourceName(stackName, alias, definition.Name, definition.External)
		existing, found := byName[name]
		if !found {
			var err error
			existing, err = c.networks.Get(ctx, name)
			if errors.Is(err, swarm.ErrNetworkNotFound) {
				return nil, fmt.Errorf("networks.%s: required network missing; reconcile it before conversion", alias)
			}
			if err != nil {
				return nil, fmt.Errorf("networks.%s: Docker metadata lookup failed", alias)
			}
			byName[name] = existing
		}
		if existing.ID == "" || existing.Name != name || existing.Scope != "swarm" {
			return nil, fmt.Errorf("networks.%s: expected an existing swarm network with exact name %s", alias, name)
		}
		result = append(result, NetworkSpec{Alias: alias, ID: existing.ID, External: definition.External,
			Spec: networkSpec(stackName, name, definition)})
	}
	return result, nil
}

func networkSpec(stackName, name string, definition compose.Network) swarm.CreateNetworkRequest {
	spec := swarm.CreateNetworkRequest{Name: name}
	if definition.External {
		return spec
	}
	spec.Driver = definition.Driver
	if spec.Driver == "" {
		spec.Driver = "overlay"
	}
	spec.Options = maps.Clone(definition.DriverOpts)
	spec.Labels = stackLabels(stackName, definition.Labels.Map)
	spec.Attachable = definition.Attachable
	if definition.Internal != nil {
		spec.Internal = *definition.Internal
	}
	return spec
}

func serviceNetworks(service compose.Service, networks []NetworkSpec) ([]dockerswarm.NetworkAttachmentConfig, error) {
	requested := []*compose.ServiceNetwork{{Alias: defaultNetwork}}
	if service.Networks != nil && len(service.Networks.List) > 0 {
		requested = service.Networks.List
	}
	result := make([]dockerswarm.NetworkAttachmentConfig, 0, len(requested))
	for _, request := range requested {
		if request == nil {
			return nil, fmt.Errorf("networks: null attachment")
		}
		index := slices.IndexFunc(networks, func(n NetworkSpec) bool { return n.Alias == request.Alias })
		if index < 0 {
			return nil, fmt.Errorf("networks.%s: undefined network", request.Alias)
		}
		if request.IPV4Address != "" {
			return nil, fmt.Errorf("networks.%s.ipv4_address: unsupported for swarm services", request.Alias)
		}
		aliases := slices.Clone(request.Aliases)
		aliases = append(aliases, service.Name)
		result = append(result, dockerswarm.NetworkAttachmentConfig{
			Target: networks[index].ID, Aliases: aliases, DriverOpts: maps.Clone(request.DriverOpts),
		})
	}
	slices.SortFunc(result, func(a, b dockerswarm.NetworkAttachmentConfig) int {
		return compareStrings(a.Target, b.Target)
	})
	return result, nil
}
