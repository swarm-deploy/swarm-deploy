package srvmappers

import (
	"strconv"
	"strings"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

type DeployMapper struct{}

func (m *DeployMapper) Map(service *compose.Service, live swarm.StackService) {
	deploy := m.mapDeploy(live.ServiceSpec)
	serviceLabels := live.Labels
	if len(serviceLabels) == 0 {
		serviceLabels = live.ServiceSpec.Labels
	}

	if len(serviceLabels) > 0 {
		deploy.Labels = *compose.NewLabels(serviceLabels)
	}

	service.Deploy = deploy
}

func (m *DeployMapper) mapDeploy(spec dockerswarm.ServiceSpec) compose.ServiceDeploy {
	deploy := compose.ServiceDeploy{
		Resources:      m.mapDeployResources(spec.TaskTemplate.Resources),
		RestartPolicy:  m.mapDeployRestartPolicy(spec.TaskTemplate.RestartPolicy),
		UpdateConfig:   m.mapDeployUpdateConfig(spec.UpdateConfig),
		RollbackConfig: m.mapDeployRollbackConfig(spec.RollbackConfig),
		Placement:      m.mapDeployPlacement(spec.TaskTemplate.Placement),
	}

	mode, replicas := m.resolveDeployMode(spec.Mode)
	deploy.Mode = mode
	deploy.Replicas = replicas

	if spec.EndpointSpec != nil && spec.EndpointSpec.Mode != "" {
		deploy.EndpointMode = string(spec.EndpointSpec.Mode)
	}

	return deploy
}

func (m *DeployMapper) resolveDeployMode(mode dockerswarm.ServiceMode) (string, *uint64) {
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

func (m *DeployMapper) mapDeployResources(resources *dockerswarm.ResourceRequirements) *compose.ServiceDeployResources {
	if resources == nil {
		return nil
	}

	mapped := &compose.ServiceDeployResources{
		Limits:       m.mapDeployLimits(resources.Limits),
		Reservations: m.mapDeployReservations(resources.Reservations),
	}

	if mapped.Limits == nil && mapped.Reservations == nil {
		return nil
	}

	return mapped
}

func (m *DeployMapper) mapDeployLimits(limits *dockerswarm.Limit) *compose.ServiceDeployResource {
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

func (m *DeployMapper) mapDeployReservations(resources *dockerswarm.Resources) *compose.ServiceDeployResource {
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

func (m *DeployMapper) mapDeployRestartPolicy(policy *dockerswarm.RestartPolicy) *compose.ServiceDeployRestartPolicy {
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

func (m *DeployMapper) mapDeployUpdateConfig(config *dockerswarm.UpdateConfig) *compose.ServiceDeployUpdateConfig {
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

func (m *DeployMapper) mapDeployRollbackConfig(config *dockerswarm.UpdateConfig) *compose.ServiceDeployRollbackConfig {
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

func (m *DeployMapper) mapDeployPlacement(placement *dockerswarm.Placement) *compose.ServiceDeployPlacement {
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
