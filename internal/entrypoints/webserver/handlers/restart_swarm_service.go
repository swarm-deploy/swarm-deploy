package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// RestartSwarmService restarts a live service without waiting for task convergence.
func (h *handler) RestartSwarmService(ctx context.Context, params generated.RestartSwarmServiceParams) error {
	service, err := h.serviceInspector.Inspect(ctx, params.ServiceID)
	if err == nil {
		_, err = h.serviceInspector.Restart(ctx, swarm.NewServiceIDReference(service.ID))
	}
	if err != nil {
		slog.ErrorContext(ctx, "[webserver] failed to restart service", "service_id", params.ServiceID, "err", err)
		status, message := http.StatusInternalServerError, "unable to restart service"
		switch {
		case errors.Is(err, swarm.ErrServiceNotFound):
			status, message = http.StatusNotFound, "service not found"
		case errors.Is(err, swarm.ErrServiceModeUnsupported):
			status, message = http.StatusBadRequest, "only replicated services can be restarted"
		case errors.Is(err, swarm.ErrServiceZeroReplicas):
			status, message = http.StatusConflict, "cannot restart a service with zero configured replicas"
		case errors.Is(err, swarm.ErrServiceUpdateConflict), errors.Is(err, swarm.ErrServiceConflict):
			status, message = http.StatusConflict, "service changed while restarting; refresh and retry"
		}
		return withStatusError(status, errors.New(message))
	}
	stack := service.Spec.Labels["com.docker.stack.namespace"]
	name := service.Spec.Name
	if stack != "" {
		name = strings.TrimPrefix(name, stack+"_")
	}
	if err = h.eventDispatcher.Publish(ctx, &events.ServiceRestarted{StackName: stack, ServiceName: name}); err != nil {
		slog.ErrorContext(ctx, "persist service restarted event after successful mutation",
			"service_id", params.ServiceID, "err", err)
	}
	return nil
}
