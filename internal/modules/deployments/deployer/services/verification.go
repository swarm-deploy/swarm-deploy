package services

import (
	"crypto/sha256"
	"encoding/hex"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"reflect"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	deploymentmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// VerificationMode describes which tasks an accepted service transition can preserve.
type VerificationMode = deploymentmodel.VerificationMode

const (
	// VerifyUnspecified marks legacy receipts without a saved mode.
	VerifyUnspecified = deploymentmodel.VerifyUnspecified
	// VerifyExistingTasks permits running tasks with the expected template.
	VerifyExistingTasks = deploymentmodel.VerifyExistingTasks
	// VerifyPlacementCompatibleTasks also permits older placement on compatible nodes.
	VerifyPlacementCompatibleTasks = deploymentmodel.VerifyPlacementCompatibleTasks
	// VerifyNewGeneration requires replacement tasks and their completed rollout.
	VerifyNewGeneration = deploymentmodel.VerifyNewGeneration
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

func updateGeneration(
	spec dockerswarm.ServiceSpec, current dockerswarm.Service, previous UpdateGeneration,
) UpdateGeneration {
	generation := UpdateGeneration{
		ServiceID: current.ID, ForceUpdate: spec.TaskTemplate.ForceUpdate, After: current.UpdatedAt,
		Mode: ClassifyVerification(current.Spec, spec), BeforeSpecDigest: verificationSpecDigest(current.Spec),
	}
	if previous.ServiceID != current.ID {
		return generation
	}
	previous = ResolveUpdateGeneration(previous, current.PreviousSpec, current.Spec)
	if previous.Mode == VerifyPlacementCompatibleTasks {
		generation.Mode = VerifyPlacementCompatibleTasks
	}
	if previous.Mode == VerifyNewGeneration {
		generation.Mode = VerifyNewGeneration
		generation.TasksAfter = previous.TasksAfter
		if generation.TasksAfter.IsZero() && current.UpdatedAt.After(previous.After) {
			// A later metadata/auth write clears UpdateStatus. Preserve its task
			// boundary while the original transition is still observable.
			generation.TasksAfter = current.UpdatedAt
			if status := current.UpdateStatus; status != nil && status.StartedAt != nil &&
				status.StartedAt.After(previous.After) {
				generation.TasksAfter = *status.StartedAt
			}
		}
	}
	return generation
}

// ResolveUpdateGeneration uses the accepted transition only while PreviousSpec
// still identifies this write. Engine/SDK normalization may preserve tasks even
// when the submitted spec appeared to require replacement. A later write must
// not erase an inherited obligation when it overwrites PreviousSpec.
func ResolveUpdateGeneration(
	generation UpdateGeneration, previous *dockerswarm.ServiceSpec, current dockerswarm.ServiceSpec,
) UpdateGeneration {
	if generation.BeforeSpecDigest == "" {
		return generation // Legacy receipt.
	}
	if previous == nil || generation.BeforeSpecDigest != verificationSpecDigest(*previous) {
		generation.Mode = VerifyNewGeneration
		generation.TasksAfter = time.Time{} // The accepted transition is no longer identifiable.
		return generation
	}
	mode := ClassifyVerification(*previous, current)
	if mode == VerifyNewGeneration {
		generation.Mode = mode
		generation.TasksAfter = time.Time{} // This write needs its own new rollout.
	} else if generation.TasksAfter.IsZero() && generation.Mode != VerifyPlacementCompatibleTasks {
		generation.Mode = mode
	}
	return generation
}

func verificationSpecDigest(spec dockerswarm.ServiceSpec) string {
	spec = cloneSpec(spec)
	normalizeDockerDefaults(&spec)
	normalizeServiceSets(&spec)
	// Keep the digest compatible with the predecessor recorded in existing attempts.
	payload, _ := json.Marshal(canonicalSpec(spec), jsonv1.DefaultOptionsV1())
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
