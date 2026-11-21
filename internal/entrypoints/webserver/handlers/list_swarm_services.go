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
		networks := service.ServiceSpec.TaskTemplate.Networks
		if len(networks) == 0 {
			networks = service.ServiceSpec.Networks //nolint:staticcheck // Older Docker services can use the service-level field.
		}
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
			UpdatedAt:      service.UpdatedAt,
			CreatedAt:      toOptDateTime(service.CreatedAt),
			NetworkTargets: make([]string, 0, len(networks)),
		}
		if service.Mode == "replicated" && service.Replicas != nil {
			item.ConfiguredReplicas.SetTo(toInt64FromUint64(*service.Replicas))
		}
		if endpoint := service.ServiceSpec.EndpointSpec; endpoint != nil {
			item.EndpointMode = toOptString(string(endpoint.Mode))
		}
		for _, port := range service.PublishedPorts {
			item.PublishedPorts = append(item.PublishedPorts, generated.SwarmServicePublishedPortsItem{
				PublishedPort: int64(port.PublishedPort), TargetPort: int64(port.TargetPort), Protocol: string(port.Protocol),
			})
		}
		if update := service.ServiceSpec.UpdateConfig; update != nil {
			item.UpdateParallelism.SetTo(toInt64FromUint64(update.Parallelism))
		}
		if update := service.UpdateStatus; update != nil {
			item.UpdateState = toOptString(string(update.State))
			item.UpdateCompletedAt = toOptDateTime(update.CompletedAt)
		}
		for _, network := range networks {
			item.NetworkTargets = append(item.NetworkTargets, network.Target)
		}
		mapped = append(mapped, item)
	}

	return &generated.SwarmServicesResponse{Services: mapped}, nil
}
