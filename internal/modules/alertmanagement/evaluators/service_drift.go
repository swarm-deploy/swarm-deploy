package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ServiceDriftEvaluator evaluates service drift, repair and removal events.
type ServiceDriftEvaluator struct{}

// Evaluate returns an action for drift that persists beyond the current reconciliation.
func (ServiceDriftEvaluator) Evaluate(_ context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.ServiceMissed:
		return serviceDrift(event.ID, source.StackName, source.ServiceName, source.Commit, "service is missing",
			source.Resolved), nil
	case *events.ServiceDrifted:
		return serviceDrift(event.ID, source.StackName, source.ServiceName, source.Commit, source.Reason,
			source.Resolved), nil
	case *events.ServiceDriftFixed:
		return []model.AlertAction{resolve(model.ServiceDriftFingerprint(source.StackName, source.ServiceName),
			event.ID, model.ResolutionReasonRecovered, "Service drift resolved")}, nil
	case *events.ServiceRemovedFromDesired:
		return []model.AlertAction{resolve(model.ServiceDriftFingerprint(source.StackName, source.ServiceName),
			event.ID, model.ResolutionReasonResourceRemoved, "Service removed from desired configuration")}, nil
	default:
		return nil, nil
	}
}

func serviceDrift(eventID, stackName, serviceName, commit, reason string, resolved bool) []model.AlertAction {
	if resolved {
		return nil
	}
	return []model.AlertAction{{
		Kind: model.RecordFailure, OpenAfter: openImmediately,
		Alert: model.Alert{
			Fingerprint: model.ServiceDriftFingerprint(stackName, serviceName), Kind: model.AlertKindServiceDrift,
			ResourceType: model.ResourceTypeService, ResourceID: stackName + "/" + serviceName,
			Title: "Service drift detected", Message: reason, Commit: commit, LatestEventID: eventID,
		},
	}}
}
