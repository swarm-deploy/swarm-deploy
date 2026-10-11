package alertmanagement

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
)

func (s *Subscriber) handleNode(ctx context.Context, eventID, nodeID string, disconnected bool) error {
	fingerprint := "node_disconnected:node:" + nodeID
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, modelstore.ErrAlertNotFound) {
		return err
	}
	now := s.now()
	if errors.Is(err, modelstore.ErrAlertNotFound) {
		if !disconnected {
			return nil
		}
		return s.store.Create(ctx, model.Alert{
			ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindNodeDisconnected,
			ResourceType: model.ResourceTypeNode, ResourceID: nodeID, Status: model.AlertStatusOpen,
			Title: "Node disconnected", Message: "Swarm node is not ready", Occurrences: 1,
			OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
		})
	}
	alert.UpdatedAt = now
	if disconnected {
		alert.Occurrences++
		alert.LatestEventID = eventID
	} else {
		alert.Status = model.AlertStatusResolved
		alert.ResolvedAt = &now
		alert.Resolution = &model.AlertResolution{
			Reason: model.ResolutionReasonRecovered, Message: "Swarm node reconnected", EventID: eventID,
		}
	}
	return s.store.Update(ctx, alert)
}
