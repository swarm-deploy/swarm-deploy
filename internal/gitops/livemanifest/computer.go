package livemanifest

import (
	"context"
	"fmt"

	"github.com/artarts36/gds"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/livemanifest/srvmappers"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Computer computes current stack live manifest from swarm state.
type Computer struct {
	serviceManager swarm.ServiceManager
	networkManager swarm.NetworkManager

	mapper srvmappers.Mapper
}

// NewComputer creates live manifest computer.
func NewComputer(serviceManager swarm.ServiceManager, networkManager swarm.NetworkManager) *Computer {
	return &Computer{
		serviceManager: serviceManager,
		networkManager: networkManager,
		mapper: srvmappers.NewComposeMapper(
			&srvmappers.VolumesMapper{},
			&srvmappers.SecretsMapper{},
			&srvmappers.ConfigsMapper{},
			&srvmappers.HealthcheckMapper{},
			&srvmappers.DeployMapper{},
			&srvmappers.PortsMapper{},
		),
	}
}

type Stack struct {
	Name     string
	Services []swarm.StackService
}

// ComputeStack computes current stack live manifest.
func (c *Computer) ComputeStack(ctx context.Context, stack Stack) (*compose.Compose, error) {
	services := make(compose.Services, 0, len(stack.Services))
	networkIDs := gds.NewSet[string]()
	for _, stackService := range stack.Services {
		mappedService, mapErr := c.mapStackServiceToCompose(stackService, networkIDs)
		if mapErr != nil {
			return nil, fmt.Errorf("map stack service %q to compose service: %w", stackService.Name, mapErr)
		}

		services = append(services, mappedService)
	}

	networks, err := c.networkManager.Map(ctx, networkIDs.List())
	if err != nil {
		return nil, fmt.Errorf("list networks %w", err)
	}

	composeNetworks := make(map[string]compose.Network)
	for _, network := range networks {
		if network.Stack == stack.Name {
			continue
		}

		composeNetworks[network.Name] = compose.Network{
			Name:     network.Name,
			Internal: ptr(network.Internal),
			External: true,
		}
	}

	for i, service := range services {
		if service.Networks == nil {
			continue
		}

		newServiceNetworks := make([]*compose.ServiceNetwork, 0, len(service.Networks.List))

		for _, network := range service.Networks.List {
			net, ok := networks[network.Alias]
			if !ok {
				continue
			}

			network.Alias = net.Name
			network.ResolvedName = net.Name
			newServiceNetworks = append(newServiceNetworks, network)
		}

		services[i].Networks = compose.NewServiceNetworks(newServiceNetworks...)
	}

	return &compose.Compose{
		Services: services,
		Networks: composeNetworks,
	}, nil
}

func (c *Computer) mapStackServiceToCompose(
	stackService swarm.StackService,
	networkIDs *gds.Set[string],
) (compose.Service, error) {
	service, err := c.mapRawServiceSpec(stackService.Name, stackService, networkIDs)
	if err != nil {
		return compose.Service{}, err
	}

	if service.Name == "" {
		service.Name = stackService.Name
	}

	if service.Image == "" {
		service.Image = stackService.Image
	}

	if service.Deploy.Mode == "" && stackService.Mode != "" {
		service.Deploy.Mode = stackService.Mode
	}

	if service.Deploy.Replicas == nil && stackService.Replicas != nil {
		replicas := *stackService.Replicas
		service.Deploy.Replicas = &replicas
	}

	return service, nil
}

func (c *Computer) mapRawServiceSpec(
	serviceName string,
	live swarm.StackService,
	networkIDs *gds.Set[string],
) (compose.Service, error) {
	service := compose.Service{
		Name: serviceName,
	}

	c.mapper.Map(&service, live)

	err := applyContainerSpec(&service, live.ServiceSpec.TaskTemplate.ContainerSpec)
	if err != nil {
		return compose.Service{}, err
	}

	if len(live.ServiceSpec.TaskTemplate.Networks) > 0 {
		service.Networks = toComposeServiceNetworks(live.ServiceSpec.TaskTemplate.Networks, networkIDs)
	}

	if live.ServiceSpec.TaskTemplate.LogDriver != nil {
		service.Logging = compose.ServiceLogging{
			Driver:  live.ServiceSpec.TaskTemplate.LogDriver.Name,
			Options: live.ServiceSpec.TaskTemplate.LogDriver.Options,
		}
	}

	return service, nil
}

func applyContainerSpec(service *compose.Service, containerSpec *dockerswarm.ContainerSpec) error {
	if containerSpec == nil {
		return nil
	}

	service.Image = containerSpec.Image
	service.Command = compose.NewCommand(append(containerSpec.Command, containerSpec.Args...))

	if len(containerSpec.Env) > 0 {
		environment, err := compose.NewEnvironment(containerSpec.Env)
		if err != nil {
			return fmt.Errorf("map environment: %w", err)
		}
		service.Environment = *environment
	}

	if len(containerSpec.Labels) > 0 {
		service.Labels = *compose.NewLabels(containerSpec.Labels)
	}

	return nil
}

func ptr[t any](v t) *t {
	return &v
}

func toComposeServiceNetworks(
	rawNetworks []dockerswarm.NetworkAttachmentConfig,
	networkIDs *gds.Set[string],
) *compose.ServiceNetworks {
	if len(rawNetworks) == 0 {
		return nil
	}

	networks := make([]*compose.ServiceNetwork, 0, len(rawNetworks))
	for _, rawNetwork := range rawNetworks {
		networks = append(networks, &compose.ServiceNetwork{
			Alias:        rawNetwork.Target,
			ResolvedName: rawNetwork.Target,
			Aliases:      rawNetwork.Aliases,
			DriverOpts:   rawNetwork.DriverOpts,
		})

		networkIDs.Add(rawNetwork.Target)
	}

	if len(networks) == 0 {
		return nil
	}

	return compose.NewServiceNetworks(networks...)
}
