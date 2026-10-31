// Package stack projects Compose definitions for single-service remediation.
package stack

import (
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// SingleServiceCompose selects a service and only its referenced top-level objects.
func SingleServiceCompose(desired compose.Compose, serviceName string) (compose.Compose, error) {
	for _, service := range desired.Services {
		if service.Name != serviceName {
			continue
		}

		desired.Services = compose.Services{service}
		desired.Networks = selectNetworks(desired.Networks, service.Networks)
		desired.Secrets = selectObjects(desired.Secrets, service.Secrets)
		desired.Configs = selectObjects(desired.Configs, service.Configs)
		desired.Volumes = selectVolumes(desired.Volumes, service.Volumes)
		return desired, nil
	}

	return compose.Compose{}, fmt.Errorf("service %q not found in compose", serviceName)
}

func selectNetworks(networks map[string]compose.Network, refs *compose.ServiceNetworks) map[string]compose.Network {
	if len(networks) == 0 {
		return nil
	}
	if refs == nil {
		defaultNetwork, ok := networks["default"]
		if !ok {
			return nil
		}
		return map[string]compose.Network{"default": defaultNetwork}
	}
	selected := make(map[string]compose.Network)
	for _, alias := range refs.GetAliases() {
		if network, ok := networks[alias]; ok {
			selected[alias] = network
		}
	}
	return selected
}

func selectObjects[T any, Objects ~map[string]*T](objects Objects, refs []compose.ObjectRef) Objects {
	if len(objects) == 0 || len(refs) == 0 {
		return nil
	}
	selected := make(Objects)
	for _, ref := range refs {
		if object, ok := objects[ref.Source]; ok {
			selected[ref.Source] = object
		}
	}
	return selected
}

func selectVolumes(volumes compose.Volumes, refs compose.ServiceVolumes) compose.Volumes {
	if len(volumes) == 0 || len(refs.Volumes) == 0 {
		return nil
	}
	selected := make(compose.Volumes)
	for _, ref := range refs.Volumes {
		if ref.Type != compose.ServiceVolumeTypeVolume {
			continue
		}
		if volume, ok := volumes[ref.Source]; ok {
			selected[ref.Source] = volume
		}
	}
	return selected
}
