package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// NodeEvaluator evaluates Swarm node connectivity events.
type NodeEvaluator struct{}

// Evaluate returns the action for a node disconnection or recovery.
func (NodeEvaluator) Evaluate(_ context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.NodeDisconnected:
		return []model.AlertAction{{
			Kind: model.RecordFailure, OpenAfter: openImmediately,
			Alert: model.Alert{
				Fingerprint: model.NodeDisconnectedFingerprint(source.NodeID), Kind: model.AlertKindNodeDisconnected,
				ResourceType: model.ResourceTypeNode, ResourceID: source.NodeID, Title: "Node disconnected",
				Message: "Swarm node is not ready", LatestEventID: event.ID,
			},
		}}, nil
	case *events.NodeConnected:
		return []model.AlertAction{resolve(model.NodeDisconnectedFingerprint(source.NodeID),
			event.ID, model.ResolutionReasonRecovered, "Swarm node reconnected")}, nil
	default:
		return nil, nil
	}
}
