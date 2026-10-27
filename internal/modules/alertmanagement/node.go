package alertmanagement

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
)

func (s *Subscriber) handleNode(ctx context.Context, eventID, nodeID string, disconnected bool) error {
	fingerprint := model.NodeDisconnectedFingerprint(nodeID)
	alert, err := s.store.FindOpenByFingerprint(ctx, fingerprint)
	if err != nil && !errors.Is(err, modelstore.ErrAlertNotFound) {
		return err
	}
	now := s.now()
	if errors.Is(err, modelstore.ErrAlertNotFound) {
		if !disconnected {
			return nil
		}
		alert = model.Alert{
			ID: s.newID(), Fingerprint: fingerprint, Kind: model.AlertKindNodeDisconnected,
			ResourceType: model.ResourceTypeNode, ResourceID: nodeID, Status: model.AlertStatusOpen,
			Title: "Node disconnected", Message: "Swarm node is not ready", Occurrences: 1,
			OpenedAt: now, UpdatedAt: now, OpenEventID: eventID, LatestEventID: eventID,
		}
		if err = s.store.Create(ctx, alert); err != nil {
			return err
		}
		return s.publishOpened(ctx, alert)
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
	if err = s.store.Update(ctx, alert); err != nil {
		return err
	}
	if !disconnected {
		return s.publishResolved(ctx, alert)
	}
	return nil
}
