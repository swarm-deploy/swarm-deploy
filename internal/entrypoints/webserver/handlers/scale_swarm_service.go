package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ScaleSwarmService updates live service replicas without waiting for task convergence.
func (h *handler) ScaleSwarmService(
	ctx context.Context,
	req *generated.ScaleSwarmServiceRequest,
	params generated.ScaleSwarmServiceParams,
) error {
	if req.Replicas < 0 {
		return withStatusError(http.StatusBadRequest, errors.New("replicas must be a non-negative integer"))
	}

	replicas := uint64(req.Replicas)
	result, err := h.serviceInspector.ScaleByID(ctx, params.ServiceID, replicas)
	if err != nil {
		switch {
		case errors.Is(err, swarm.ErrServiceNotFound):
			return withStatusError(http.StatusNotFound, err)
		case errors.Is(err, swarm.ErrServiceModeUnsupported):
			return withStatusError(http.StatusBadRequest, err)
		case errors.Is(err, swarm.ErrServiceUpdateConflict):
			return withStatusError(http.StatusConflict, err)
		default:
			slog.ErrorContext(ctx, "[webserver] failed to scale service", "service_id", params.ServiceID, "err", err)
			return withStatusError(http.StatusInternalServerError, errors.New("unable to scale service"))
		}
	}

	var event events.Event
	if replicas > result.PreviousReplicas {
		event = &events.ServiceReplicasIncreased{
			StackName: result.Stack, ServiceName: result.Service,
			PreviousReplicas: result.PreviousReplicas, CurrentReplicas: replicas,
		}
	} else if replicas < result.PreviousReplicas {
		event = &events.ServiceReplicasDecreased{
			StackName: result.Stack, ServiceName: result.Service,
			PreviousReplicas: result.PreviousReplicas, CurrentReplicas: replicas,
		}
	}
	if event != nil {
		if err = h.eventDispatcher.Publish(ctx, event); err != nil {
			slog.ErrorContext(ctx, "persist service replicas event after successful mutation",
				"service_id", params.ServiceID, "err", err)
		}
	}
	return nil
}
