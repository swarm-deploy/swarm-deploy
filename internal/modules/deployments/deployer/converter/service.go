package converter

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/distribution/reference"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/google/shlex"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
)

func convertService(
	stackName string, service compose.Service, volumes compose.Volumes, networks []NetworkSpec, objects resources.Resolved,
) (dockerswarm.ServiceSpec, error) {
	spec := dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{
		Name: stackName + "_" + service.Name, Labels: stackLabels(stackName, service.Deploy.Labels.Map),
	}}
	spec.Labels["com.docker.stack.image"] = service.Image
	container := &dockerswarm.ContainerSpec{Image: service.Image, Labels: stackLabels(stackName, service.Labels.Map)}
	if service.StopGracePeriod != "" {
		grace, err := duration(service.StopGracePeriod)
		if err != nil {
			return spec, fmt.Errorf("stop_grace_period: %w", err)
		}
		container.StopGracePeriod = &grace
	}
	var err error
	container.Args, err = commandWords(service.Command)
	if err != nil {
		return spec, fmt.Errorf("command: invalid quoting")
	}
	container.Command, err = commandWords(service.Entrypoint)
	if err != nil {
		return spec, fmt.Errorf("entrypoint: invalid quoting")
	}
	container.CapabilityAdd, container.CapabilityDrop = capabilities(service.CapAdd, service.CapDrop)
	for _, key := range sortedKeys(service.Environment.Map) {
		value := key + "=" + service.Environment.Map[key]
		if slices.Contains(service.Environment.Unset, key) {
			value = key
		}
		container.Env = append(container.Env, value)
	}
	if service.Healthcheck != nil {
		container.Healthcheck, err = healthcheck(service.Healthcheck)
		if err != nil {
			return spec, err
		}
	}
	container.Mounts, err = serviceMounts(stackName, service, volumes)
	if err != nil {
		return spec, err
	}
	if err = serviceObjects(container, service, objects); err != nil {
		return spec, err
	}
	if err = containerOptions(container, service, objects); err != nil {
		return spec, err
	}
	spec.TaskTemplate.ContainerSpec = container
	spec.TaskTemplate.Networks, err = serviceNetworks(service, networks)
	if err != nil {
		return spec, err
	}
	if service.Logging.Driver != "" {
		spec.TaskTemplate.LogDriver = &dockerswarm.Driver{
			Name: service.Logging.Driver, Options: maps.Clone(service.Logging.Options),
		}
	} else if len(service.Logging.Options) > 0 {
		return spec, fmt.Errorf("logging.options: driver is required")
	}
	if err = deployment(&spec, service.Deploy); err != nil {
		return spec, err
	}
	spec.EndpointSpec, err = endpoint(service)
	return spec, err
}

func commandWords(command compose.Command) ([]string, error) {
	var words []string
	switch {
	case command.IsList() || len(command.Args) == 0:
		words = slices.Clone(command.Args)
	case len(command.Args) != 1:
		return nil, fmt.Errorf("invalid scalar command")
	default:
		var err error
		// Compose scalar commands use shell tokenization, without invoking a shell.
		words, err = shlex.Split(command.Args[0])
		if err != nil {
			return nil, err
		}
	}
	return words, nil
}

func capabilities(add, drop []string) ([]string, []string) {
	added, dropped := normalizeCapabilities(add), normalizeCapabilities(drop)
	dropped = slices.DeleteFunc(dropped, func(value string) bool { return slices.Contains(added, value) })
	return added, dropped
}

func normalizeCapabilities(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "RESET" {
			continue
		}
		if value == "ALL" {
			return []string{"ALL"}
		}
		if !strings.HasPrefix(value, "CAP_") {
			value = "CAP_" + value
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func endpoint(service compose.Service) (*dockerswarm.EndpointSpec, error) {
	mode := service.Deploy.EndpointMode
	if mode != "" && mode != "vip" && mode != "dnsrr" {
		return nil, fmt.Errorf("deploy.endpoint_mode: unsupported value")
	}
	result := &dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionMode(mode)}
	for _, port := range service.Ports.Ports {
		converted, err := convertPort(port, mode)
		if err != nil {
			return nil, err
		}
		result.Ports = append(result.Ports, converted)
	}

	slices.SortStableFunc(result.Ports, func(a, b dockerswarm.PortConfig) int {
		if a.PublishedPort < b.PublishedPort {
			return -1
		}
		if a.PublishedPort > b.PublishedPort {
			return 1
		}
		return 0
	})
	return result, nil
}

func convertPort(port compose.ServicePort, endpointMode string) (dockerswarm.PortConfig, error) {
	result := dockerswarm.PortConfig{}
	if port.Target < 1 || port.Target > maxPort || port.Published < 0 || port.Published > maxPort {
		return result, fmt.Errorf("ports: target must be 1..65535 and published 0..65535")
	}
	if port.HostIP != "" || port.AppProtocol != "" {
		return result, fmt.Errorf("ports.host_ip/app_protocol: unsupported by swarm")
	}
	protocol := port.Protocol
	if protocol == "" {
		protocol = dockerswarm.PortConfigProtocolTCP
	}
	if !slices.Contains([]dockerswarm.PortConfigProtocol{"tcp", "udp", "sctp"}, protocol) {
		return result, fmt.Errorf("ports.protocol: unsupported value")
	}
	mode := port.Mode
	if mode == "" {
		mode = dockerswarm.PortConfigPublishModeIngress
	}
	if mode != "ingress" && mode != "host" {
		return result, fmt.Errorf("ports.mode: unsupported value")
	}
	if endpointMode == "dnsrr" && mode == "ingress" {
		return result, fmt.Errorf("ports.mode: ingress is incompatible with deploy.endpoint_mode dnsrr")
	}
	return dockerswarm.PortConfig{Protocol: protocol, TargetPort: uint32(port.Target),
		PublishedPort: uint32(port.Published), PublishMode: mode}, nil
}

const maxPort = 65535

func validateImage(image string) error {
	if image == "" {
		return fmt.Errorf("image: required")
	}
	if _, err := reference.ParseNormalizedNamed(image); err != nil {
		return fmt.Errorf("image: invalid reference")
	}
	return nil
}
