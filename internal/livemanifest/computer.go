package livemanifest

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/artarts36/gds"

	container "github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const nanoCPUToCPUScale = 1_000_000_000

// Computer computes current stack live manifest from swarm state.
type Computer struct {
	serviceManager swarm.ServiceManager
	networkManager swarm.NetworkManager
}

// NewComputer creates live manifest computer.
func NewComputer(serviceManager swarm.ServiceManager, networkManager swarm.NetworkManager) *Computer {
	return &Computer{
		serviceManager: serviceManager,
		networkManager: networkManager,
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
		mappedService, mapErr := mapStackServiceToCompose(stackService, networkIDs)
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

func mapStackServiceToCompose(stackService swarm.StackService, networkIDs *gds.Set[string]) (compose.Service, error) {
	service, err := mapRawServiceSpec(stackService.Name, stackService.ServiceSpec, networkIDs)
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

func mapRawServiceSpec(
	serviceName string,
	spec dockerswarm.ServiceSpec,
	networkIDs *gds.Set[string],
) (compose.Service, error) {
	service := compose.Service{
		Name: serviceName,
	}

	err := applyContainerSpec(&service, spec.TaskTemplate.ContainerSpec)
	if err != nil {
		return compose.Service{}, err
	}

	if spec.EndpointSpec != nil && len(spec.EndpointSpec.Ports) > 0 {
		service.Ports = toComposePorts(spec.EndpointSpec.Ports)
	}

	if len(spec.TaskTemplate.Networks) > 0 {
		service.Networks = toComposeServiceNetworks(spec.TaskTemplate.Networks, networkIDs)
	}

	if spec.TaskTemplate.LogDriver != nil {
		service.Logging = compose.ServiceLogging{
			Driver:  spec.TaskTemplate.LogDriver.Name,
			Options: spec.TaskTemplate.LogDriver.Options,
		}
	}

	deploy := toComposeDeploy(spec)
	if len(spec.Labels) > 0 {
		deploy.Labels = *compose.NewLabels(spec.Labels)
	}
	service.Deploy = deploy

	return service, nil
}

func applyContainerSpec(service *compose.Service, containerSpec *dockerswarm.ContainerSpec) error {
	if containerSpec == nil {
		return nil
	}

	service.Image = containerSpec.Image
	service.Command = compose.NewCommand(append(containerSpec.Command, containerSpec.Args...))
	service.Secrets = toComposeSecrets(containerSpec.Secrets)
	service.Configs = toComposeConfigs(containerSpec.Configs)
	service.Healthcheck = toComposeHealthcheck(containerSpec.Healthcheck)

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

func toComposeSecrets(rawRefs []*dockerswarm.SecretReference) []compose.ObjectRef {
	if len(rawRefs) == 0 {
		return nil
	}

	mapped := make([]compose.ObjectRef, 0, len(rawRefs))
	for _, rawRef := range rawRefs {
		if rawRef == nil {
			continue
		}

		ref := compose.ObjectRef{
			Source: buildObjectRefSource(rawRef.SecretName, rawRef.SecretID),
		}

		if rawRef.File != nil {
			ref.Target = rawRef.File.Name
			ref.Mode = ptr(rawRef.File.Mode)
			ref.Gid = rawRef.File.GID
			ref.Uid = rawRef.File.UID
		}

		mapped = append(mapped, ref)
	}

	if len(mapped) == 0 {
		return nil
	}

	return mapped
}

func buildObjectRefSource(name string, id string) string {
	source := name
	if source == "" {
		source = id
	} else if id != "" {
		source += ":" + id
	}

	if source == "" {
		return "unknown"
	}

	return source
}

func toComposeConfigs(rawRefs []*dockerswarm.ConfigReference) []compose.ObjectRef {
	if len(rawRefs) == 0 {
		return nil
	}

	mapped := make([]compose.ObjectRef, 0, len(rawRefs))
	for _, rawRef := range rawRefs {
		if rawRef == nil {
			continue
		}

		ref := compose.ObjectRef{
			Source: buildObjectRefSource(rawRef.ConfigName, rawRef.ConfigID),
		}

		if rawRef.File != nil {
			ref.Target = rawRef.File.Name
			ref.Mode = ptr(rawRef.File.Mode)
			ref.Gid = rawRef.File.GID
			ref.Uid = rawRef.File.UID
		}

		mapped = append(mapped, ref)
	}

	if len(mapped) == 0 {
		return nil
	}

	return mapped
}

func toComposeHealthcheck(healthcheck *container.HealthConfig) *compose.ServiceHealth {
	if healthcheck == nil {
		return nil
	}

	mapped := &compose.ServiceHealth{}
	hasData := false

	if len(healthcheck.Test) == 1 && strings.EqualFold(healthcheck.Test[0], "NONE") {
		mapped.Disable = true
		hasData = true
	} else if len(healthcheck.Test) > 0 {
		mapped.Test = compose.NewCommand(healthcheck.Test)
		hasData = true
	}

	if healthcheck.Interval > 0 {
		mapped.Interval = healthcheck.Interval.String()
		hasData = true
	}

	if healthcheck.Timeout > 0 {
		mapped.Timeout = healthcheck.Timeout.String()
		hasData = true
	}

	if healthcheck.StartPeriod > 0 {
		mapped.StartPeriod = healthcheck.StartPeriod.String()
		hasData = true
	}

	if healthcheck.StartInterval > 0 {
		mapped.StartInterval = healthcheck.StartInterval.String()
		hasData = true
	}

	if healthcheck.Retries > 0 {
		retries := uint64(healthcheck.Retries)
		mapped.Retries = &retries
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func toComposePorts(rawPorts []dockerswarm.PortConfig) compose.ServicePorts {
	ports := compose.ServicePorts{
		Ports: make([]compose.ServicePort, 0, len(rawPorts)),
	}

	for _, rawPort := range rawPorts {
		protocol := compose.PortProtocol(strings.ToLower(string(rawPort.Protocol)))
		if !protocol.Valid() {
			protocol = compose.PortProtocolTCP
		}

		port := compose.ServicePort{
			Published: int(rawPort.PublishedPort),
			Target:    int(rawPort.TargetPort),
			Protocol:  protocol,
		}

		if rawPort.PublishMode != "" {
			port.Mode = strings.ToLower(string(rawPort.PublishMode))
		}

		ports.Ports = append(ports.Ports, port)
	}

	return ports
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

func toComposeDeploy(spec dockerswarm.ServiceSpec) compose.ServiceDeploy {
	deploy := compose.ServiceDeploy{
		Resources:      toComposeDeployResources(spec.TaskTemplate.Resources),
		RestartPolicy:  toComposeDeployRestartPolicy(spec.TaskTemplate.RestartPolicy),
		UpdateConfig:   toComposeDeployUpdateConfig(spec.UpdateConfig),
		RollbackConfig: toComposeDeployRollbackConfig(spec.RollbackConfig),
		Placement:      toComposeDeployPlacement(spec.TaskTemplate.Placement),
	}

	mode, replicas := resolveComposeDeployMode(spec.Mode)
	deploy.Mode = mode
	deploy.Replicas = replicas

	if spec.EndpointSpec != nil && spec.EndpointSpec.Mode != "" {
		deploy.EndpointMode = string(spec.EndpointSpec.Mode)
	}

	return deploy
}

func resolveComposeDeployMode(mode dockerswarm.ServiceMode) (string, *uint64) {
	switch {
	case mode.Replicated != nil:
		replicas := uint64(0)
		if mode.Replicated.Replicas != nil {
			replicas = *mode.Replicated.Replicas
		}
		return "replicated", &replicas
	case mode.Global != nil:
		return "global", nil
	case mode.ReplicatedJob != nil:
		return "replicated-job", nil
	case mode.GlobalJob != nil:
		return "global-job", nil
	default:
		return "", nil
	}
}

func toComposeDeployResources(resources *dockerswarm.ResourceRequirements) *compose.ServiceDeployResources {
	if resources == nil {
		return nil
	}

	mapped := &compose.ServiceDeployResources{
		Limits:       toComposeDeployLimits(resources.Limits),
		Reservations: toComposeDeployReservations(resources.Reservations),
	}

	if mapped.Limits == nil && mapped.Reservations == nil {
		return nil
	}

	return mapped
}

func toComposeDeployLimits(limits *dockerswarm.Limit) *compose.ServiceDeployResource {
	if limits == nil {
		return nil
	}

	mapped := &compose.ServiceDeployResource{}
	hasData := false

	if limits.NanoCPUs > 0 {
		mapped.Cpus = formatNanoCPUs(limits.NanoCPUs)
		hasData = true
	}
	if limits.MemoryBytes > 0 {
		mapped.Memory = strconv.FormatInt(limits.MemoryBytes, 10)
		hasData = true
	}
	if limits.Pids > 0 {
		pids := uint64(limits.Pids)
		mapped.Pids = &pids
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func toComposeDeployReservations(resources *dockerswarm.Resources) *compose.ServiceDeployResource {
	if resources == nil {
		return nil
	}

	mapped := &compose.ServiceDeployResource{}
	hasData := false

	if resources.NanoCPUs > 0 {
		mapped.Cpus = formatNanoCPUs(resources.NanoCPUs)
		hasData = true
	}
	if resources.MemoryBytes > 0 {
		mapped.Memory = strconv.FormatInt(resources.MemoryBytes, 10)
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func formatNanoCPUs(nanoCPUs int64) string {
	value := float64(nanoCPUs) / nanoCPUToCPUScale
	formatted := strconv.FormatFloat(value, 'f', 9, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}

	return formatted
}

func toComposeDeployRestartPolicy(policy *dockerswarm.RestartPolicy) *compose.ServiceDeployRestartPolicy {
	if policy == nil {
		return nil
	}

	mapped := &compose.ServiceDeployRestartPolicy{}
	hasData := false

	if policy.Condition != "" {
		mapped.Condition = string(policy.Condition)
		hasData = true
	}
	if policy.Delay != nil && *policy.Delay > 0 {
		mapped.Delay = policy.Delay.String()
		hasData = true
	}
	if policy.MaxAttempts != nil {
		maxAttempts := *policy.MaxAttempts
		mapped.MaxAttempts = &maxAttempts
		hasData = true
	}
	if policy.Window != nil && *policy.Window > 0 {
		mapped.Window = policy.Window.String()
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func toComposeDeployUpdateConfig(config *dockerswarm.UpdateConfig) *compose.ServiceDeployUpdateConfig {
	if config == nil {
		return nil
	}

	mapped := &compose.ServiceDeployUpdateConfig{}
	hasData := false

	if config.Parallelism > 0 {
		parallelism := config.Parallelism
		mapped.Parallelism = &parallelism
		hasData = true
	}
	if config.Delay > 0 {
		mapped.Delay = config.Delay.String()
		hasData = true
	}
	if config.FailureAction != "" {
		mapped.FailureAction = config.FailureAction
		hasData = true
	}
	if config.Monitor > 0 {
		mapped.Monitor = config.Monitor.String()
		hasData = true
	}
	if config.MaxFailureRatio > 0 {
		maxFailureRatio := float64(config.MaxFailureRatio)
		mapped.MaxFailureRatio = &maxFailureRatio
		hasData = true
	}
	if config.Order != "" {
		mapped.Order = config.Order
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func toComposeDeployRollbackConfig(config *dockerswarm.UpdateConfig) *compose.ServiceDeployRollbackConfig {
	if config == nil {
		return nil
	}

	mapped := &compose.ServiceDeployRollbackConfig{}
	hasData := false

	if config.Parallelism > 0 {
		parallelism := config.Parallelism
		mapped.Parallelism = &parallelism
		hasData = true
	}
	if config.Delay > 0 {
		mapped.Delay = config.Delay.String()
		hasData = true
	}
	if config.FailureAction != "" {
		mapped.FailureAction = config.FailureAction
		hasData = true
	}
	if config.Monitor > 0 {
		mapped.Monitor = config.Monitor.String()
		hasData = true
	}
	if config.MaxFailureRatio > 0 {
		maxFailureRatio := float64(config.MaxFailureRatio)
		mapped.MaxFailureRatio = &maxFailureRatio
		hasData = true
	}
	if config.Order != "" {
		mapped.Order = config.Order
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}

func toComposeDeployPlacement(placement *dockerswarm.Placement) *compose.ServiceDeployPlacement {
	if placement == nil {
		return nil
	}

	mapped := &compose.ServiceDeployPlacement{
		Constraints: placement.Constraints,
	}
	hasData := len(mapped.Constraints) > 0

	if len(placement.Preferences) > 0 {
		preferences := make([]compose.ServiceDeployPlacementPreference, 0, len(placement.Preferences))
		for _, preference := range placement.Preferences {
			if preference.Spread == nil {
				continue
			}

			descriptor := strings.TrimSpace(preference.Spread.SpreadDescriptor)
			if descriptor == "" {
				continue
			}

			preferences = append(preferences, compose.ServiceDeployPlacementPreference{
				Spread: descriptor,
			})
		}

		if len(preferences) > 0 {
			mapped.Preferences = preferences
			hasData = true
		}
	}

	if placement.MaxReplicas > 0 {
		maxReplicas := placement.MaxReplicas
		mapped.MaxReplicasPerNode = &maxReplicas
		hasData = true
	}

	if !hasData {
		return nil
	}

	return mapped
}
