package stack

import (
	"fmt"
	"os"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"go.yaml.in/yaml/v3"
)

// PreparedService owns the temporary Compose file for one selected service.
type PreparedService struct {
	Desired compose.Compose
	Path    string
}

// PrepareService selects one service and writes its deploy Compose to a temporary file.
func PrepareService(desired, deploy compose.Compose, serviceName string) (*PreparedService, error) {
	serviceDesired, err := SingleServiceCompose(desired, serviceName)
	if err != nil {
		return nil, err
	}
	serviceDeploy, err := SingleServiceCompose(deploy, serviceName)
	if err != nil {
		return nil, err
	}

	payload, err := yaml.Marshal(serviceDeploy)
	if err != nil {
		return nil, fmt.Errorf("marshal single service compose: %w", err)
	}

	file, err := os.CreateTemp("", "swarm-deploy-single-service-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temporary compose file: %w", err)
	}
	path := file.Name()
	if _, err = file.Write(payload); err != nil {
		_ = file.Close()
		removeTemporaryCompose(path)
		return nil, fmt.Errorf("write temporary compose file: %w", err)
	}
	if err = file.Close(); err != nil {
		removeTemporaryCompose(path)
		return nil, fmt.Errorf("close temporary compose file: %w", err)
	}

	return &PreparedService{Desired: serviceDesired, Path: path}, nil
}

// Cleanup removes the temporary Compose file.
func (p *PreparedService) Cleanup() {
	if p != nil && p.Path != "" {
		removeTemporaryCompose(p.Path)
	}
}

func removeTemporaryCompose(path string) {
	// #nosec G703 -- path is returned by os.CreateTemp and retained without user modification.
	_ = os.Remove(path)
}

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
