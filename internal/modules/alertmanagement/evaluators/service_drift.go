package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// ServiceDriftEvaluator evaluates service drift, repair and removal events.
type ServiceDriftEvaluator struct {
	store modelstore.Store
}

// NewServiceDriftEvaluator reads open service drift incidents.
func NewServiceDriftEvaluator(store modelstore.Store) *ServiceDriftEvaluator {
	return &ServiceDriftEvaluator{store: store}
}

// Evaluate returns an action for drift that persists beyond the current reconciliation.
func (e *ServiceDriftEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.ServiceMissed:
		return e.serviceDrift(ctx, event.ID, source.StackName, source.ServiceName, source.Commit, "service is missing",
			source.Resolved)
	case *events.ServiceDrifted:
		return e.serviceDrift(ctx, event.ID, source.StackName, source.ServiceName, source.Commit, source.Reason,
			source.Resolved)
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

func (e *ServiceDriftEvaluator) serviceDrift(
	ctx context.Context, eventID, stackName, serviceName, commit, reason string, resolved bool,
) ([]model.AlertAction, error) {
	if resolved {
		return nil, nil
	}
	alert, err := alertForFailure(ctx, e.store, model.Alert{
		Fingerprint: model.ServiceDriftFingerprint(stackName, serviceName), Kind: model.AlertKindServiceDrift,
		ResourceType: model.ResourceTypeService, ResourceID: stackName + "/" + serviceName,
		Title: "Service drift detected",
	})
	if err != nil {
		return nil, err
	}
	alert.Message, alert.Commit, alert.LatestEventID = reason, commit, eventID
	return []model.AlertAction{{Kind: model.RecordFailure, Alert: alert, OpenAfter: openImmediately}}, nil
}
