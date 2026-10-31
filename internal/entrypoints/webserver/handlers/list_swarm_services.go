package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ListSwarmServices returns live services directly from Docker Swarm.
func (h *handler) ListSwarmServices(ctx context.Context) (*generated.SwarmServicesResponse, error) {
	services, err := h.serviceInspector.ListServices(ctx, swarm.ListServicesFilter{})
	if err != nil {
		slog.ErrorContext(ctx, "[webserver] failed to list swarm services", slog.Any("err", err))

		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to list swarm services"))
	}

	mapped := make([]generated.SwarmService, 0, len(services))
	for _, service := range services {
		item := generated.SwarmService{
			ID:       service.ID,
			Name:     service.Name,
			FullName: service.FullName,
			Stack:    service.Stack,
			Image:    service.Image,
			Mode:     service.Mode,
			Replicas: generated.SwarmServiceReplicas{
				Running: toInt64FromUint64(service.RunningTasks),
				Desired: toInt64FromUint64(service.DesiredTasks),
			},
			UpdatedAt: service.UpdatedAt,
		}
		if service.Mode == "replicated" && service.Replicas != nil {
			item.ConfiguredReplicas.SetTo(toInt64FromUint64(*service.Replicas))
		}
		mapped = append(mapped, item)
	}

	return &generated.SwarmServicesResponse{Services: mapped}, nil
}
