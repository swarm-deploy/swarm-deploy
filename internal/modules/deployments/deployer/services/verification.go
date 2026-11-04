package services

import (
	"reflect"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// VerificationMode describes which tasks an accepted service transition can preserve.
type VerificationMode uint8

const (
	// VerifyExistingTasks permits running tasks with the expected template.
	VerifyExistingTasks VerificationMode = iota
	// VerifyPlacementCompatibleTasks also permits older placement on compatible nodes.
	VerifyPlacementCompatibleTasks
	// VerifyNewGeneration requires replacement tasks and their completed rollout.
	VerifyNewGeneration
)

// ClassifyVerification mirrors SwarmKit's IsTaskDirty ordering. Placement-only
// task changes preserve compatible tasks even when the endpoint also changes.
// Classification does not establish convergence: running tasks and rollout status
// still have to be verified by the waiter.
func ClassifyVerification(before, after dockerswarm.ServiceSpec) VerificationMode {
	comparison := swarm.NewTaskSpec(before.TaskTemplate).CompareServiceTemplate(after.TaskTemplate)
	if comparison.PlacementOnly() {
		return VerifyPlacementCompatibleTasks
	}
	if !comparison.Equal() || !reflect.DeepEqual(
		normalizeEndpoint(before.EndpointSpec), normalizeEndpoint(after.EndpointSpec),
	) {
		return VerifyNewGeneration
	}
	return VerifyExistingTasks
}

func normalizeEndpoint(endpoint *dockerswarm.EndpointSpec) dockerswarm.EndpointSpec {
	result := dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionModeVIP}
	if endpoint == nil {
		return result
	}
	if endpoint.Mode != "" {
		result.Mode = endpoint.Mode
	}
	for _, port := range endpoint.Ports {
		if port.Protocol == "" {
			port.Protocol = dockerswarm.PortConfigProtocolTCP
		}
		if port.PublishMode == "" {
			port.PublishMode = dockerswarm.PortConfigPublishModeIngress
		}
		result.Ports = append(result.Ports, port)
	}
	return result
}
