package evaluators

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

// NodeEvaluator evaluates Swarm node connectivity events.
type NodeEvaluator struct {
	store modelstore.Store
}

// NewNodeEvaluator reads open node incidents.
func NewNodeEvaluator(store modelstore.Store) *NodeEvaluator {
	return &NodeEvaluator{store: store}
}

// Evaluate returns the action for a node disconnection or recovery.
func (e *NodeEvaluator) Evaluate(ctx context.Context, event events.Envelope) ([]model.AlertAction, error) {
	switch source := event.Event.(type) {
	case *events.NodeDisconnected:
		alert, err := alertForFailure(ctx, e.store, model.Alert{
			Fingerprint: model.NodeDisconnectedFingerprint(source.NodeID), Kind: model.AlertKindNodeDisconnected,
			ResourceType: model.ResourceTypeNode, ResourceID: source.NodeID, Title: "Node disconnected",
			Message: "Swarm node is not ready",
		})
		if err != nil {
			return nil, err
		}
		alert.LatestEventID = event.ID
		return []model.AlertAction{{Kind: model.RecordFailure, Alert: alert, OpenAfter: openImmediately}}, nil
	case *events.NodeConnected:
		return []model.AlertAction{resolve(model.NodeDisconnectedFingerprint(source.NodeID),
			event.ID, model.ResolutionReasonRecovered, "Swarm node reconnected")}, nil
	default:
		return nil, nil
	}
}
