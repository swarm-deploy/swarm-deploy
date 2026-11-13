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
	gitopsStack, hasStack := req.GitopsStack.Get()
	gitopsService, hasService := req.GitopsService.Get()
	if hasStack != hasService {
		return withStatusError(http.StatusBadRequest, errors.New("gitops_stack and gitops_service must be supplied together"))
	}
	if hasStack {
		pauseState, pauseErr := h.syncPauses.GetService(ctx, gitopsStack, gitopsService)
		if pauseErr != nil {
			return pauseHTTPError(pauseErr)
		}
		if !pauseState.Effective {
			return withStatusError(http.StatusConflict, errors.New("GitOps-managed service must be paused before scaling"))
		}
		if waitErr := h.control.WaitStackIdle(ctx, gitopsStack); waitErr != nil {
			return withStatusError(http.StatusConflict, errors.New("current stack operation did not finish"))
		}
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
