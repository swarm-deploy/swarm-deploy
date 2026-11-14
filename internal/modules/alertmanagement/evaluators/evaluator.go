package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// Evaluator derives alert actions from a source event.
type Evaluator interface {
	// Evaluate returns the actions in the order in which they should be applied.
	Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error)
}

const (
	openImmediately           uint64 = 1
	temporaryFailureThreshold uint64 = 3
)

func resetFailureStreak(fingerprint string) model.AlertAction {
	return model.AlertAction{
		Kind:  model.ResetFailureStreak,
		Alert: model.Alert{Fingerprint: fingerprint},
	}
}

func resolve(fingerprint, eventID string, reason model.ResolutionReason, message string) model.AlertAction {
	return model.AlertAction{
		Kind: model.Resolve,
		Alert: model.Alert{
			Fingerprint: fingerprint,
			Resolution:  &model.AlertResolution{Reason: reason, Message: message, EventID: eventID},
		},
	}
}
