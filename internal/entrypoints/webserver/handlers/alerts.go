package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
)

func (h *handler) ListAlerts(
	ctx context.Context,
	params generated.ListAlertsParams,
) (*generated.AlertsResponse, error) {
	alerts, err := h.alerts.List(ctx, modelstore.ListFilter{
		Status: alertmodel.AlertStatus(params.Status.Or("")),
		Limit:  int(params.Limit.Or(0)),
	})
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	return &generated.AlertsResponse{Alerts: toGeneratedAlerts(alerts)}, nil
}

func (h *handler) GetAlert(ctx context.Context, params generated.GetAlertParams) (*generated.Alert, error) {
	alert, err := h.alerts.Get(ctx, params.ID)
	if errors.Is(err, modelstore.ErrAlertNotFound) {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("alert %q not found", params.ID))
	}
	if err != nil {
		return nil, fmt.Errorf("get alert: %w", err)
	}
	mapped := toGeneratedAlert(alert)
	return &mapped, nil
}

func toGeneratedAlerts(alerts []alertmodel.Alert) []generated.Alert {
	result := make([]generated.Alert, 0, len(alerts))
	for _, alert := range alerts {
		result = append(result, toGeneratedAlert(alert))
	}
	return result
}

func toGeneratedAlert(alert alertmodel.Alert) generated.Alert {
	result := generated.Alert{
		ID: alert.ID, Fingerprint: alert.Fingerprint, Kind: generated.AlertKind(alert.Kind),
		ResourceType: generated.AlertResourceType(alert.ResourceType), ResourceId: alert.ResourceID,
		Status: generated.AlertStatus(alert.Status), Title: alert.Title, Message: alert.Message,
		Occurrences: generatedOccurrences(alert.Occurrences), OpenedAt: alert.OpenedAt, UpdatedAt: alert.UpdatedAt,
		OpenEventId: alert.OpenEventID, LatestEventId: alert.LatestEventID,
	}
	if alert.ResolvedAt != nil {
		result.ResolvedAt = generated.NewOptDateTime(*alert.ResolvedAt)
	}
	if alert.Resolution != nil {
		result.Resolution = generated.NewOptAlertResolution(generated.AlertResolution{
			Reason:  generated.AlertResolutionReason(alert.Resolution.Reason),
			Message: alert.Resolution.Message, EventId: alert.Resolution.EventID,
		})
	}
	return result
}

func generatedOccurrences(occurrences uint64) int64 {
	if occurrences > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(occurrences)
}
