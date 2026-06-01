package srvmappers

import (
	"strings"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type PortsMapper struct{}

func (m *PortsMapper) Map(service *compose.Service, live swarm.StackService) {
	if live.ServiceSpec.EndpointSpec == nil || len(live.ServiceSpec.EndpointSpec.Ports) == 0 {
		return
	}

	service.Ports = m.mapPorts(live.ServiceSpec.EndpointSpec.Ports)
}

func (m *PortsMapper) mapPorts(rawPorts []dockerswarm.PortConfig) compose.ServicePorts {
	ports := compose.ServicePorts{
		Ports: make([]compose.ServicePort, 0, len(rawPorts)),
	}

	for _, rawPort := range rawPorts {
		port := compose.ServicePort{
			Published: int(rawPort.PublishedPort),
			Target:    int(rawPort.TargetPort),
			Protocol:  rawPort.Protocol,
		}

		if rawPort.PublishMode != "" {
			port.Mode = strings.ToLower(string(rawPort.PublishMode))
		}

		ports.Ports = append(ports.Ports, port)
	}

	return ports
}
