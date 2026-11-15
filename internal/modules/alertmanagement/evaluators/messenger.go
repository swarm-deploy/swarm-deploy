package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// MessengerEvaluator evaluates messenger availability and channel configuration.
type MessengerEvaluator struct {
	store modelstore.Store
}

// NewMessengerEvaluator reads open messenger incidents.
func NewMessengerEvaluator(store modelstore.Store) *MessengerEvaluator {
	return &MessengerEvaluator{store: store}
}

// Evaluate returns the action for a messenger failure or recovery.
func (e *MessengerEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.MessengerUnavailable:
		return e.evaluateFailure(ctx, event.ID, model.AlertKindMessengerUnavailable,
			source.ResourceID, "Messenger unavailable", source.Description, source.ErrorCode)
	case *events.MessengerAvailable:
		return []model.AlertAction{model.NewResolveAction(
			model.MessengerFingerprint(model.AlertKindMessengerUnavailable, source.ResourceID),
			event.ID, model.ResolutionReasonRecovered, "Messenger connectivity recovered",
		)}, nil
	case *events.MessengerMisconfigured:
		return e.evaluateFailure(ctx, event.ID, model.AlertKindMessengerMisconfigured,
			source.ResourceID, "Messenger misconfigured", source.Description, source.ErrorCode)
	case *events.MessengerDeliverySucceeded:
		return []model.AlertAction{model.NewResolveAction(
			model.MessengerFingerprint(model.AlertKindMessengerMisconfigured, source.ResourceID),
			event.ID, model.ResolutionReasonRecovered, "Messenger configuration recovered",
		)}, nil
	default:
		return nil, nil
	}
}

func (e *MessengerEvaluator) evaluateFailure(
	ctx context.Context, eventID string, kind model.AlertKind, resourceID, title, message, errorCode string,
) ([]model.AlertAction, error) {
	alert, err := alertForFailure(ctx, e.store, model.Alert{
		Fingerprint: model.MessengerFingerprint(kind, resourceID), Kind: kind,
		ResourceType: model.ResourceTypeMessenger, ResourceID: resourceID,
	})
	if err != nil {
		return nil, err
	}
	alert.Title, alert.Message, alert.ErrorCode, alert.LatestEventID = title, message, errorCode, eventID
	return []model.AlertAction{model.NewRecordFailureAction(alert, openImmediately, false)}, nil
}
