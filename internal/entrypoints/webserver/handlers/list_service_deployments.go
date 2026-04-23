package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const serviceDeploymentsLimit = 5

func (h *handler) ListServiceDeployments(
	ctx context.Context,
	params generated.ListServiceDeploymentsParams,
) (*generated.ServiceDeploymentsResponse, error) {
	status, err := h.serviceInspector.GetStatus(ctx, swarm.NewServiceReference(params.Stack, params.Service))
	if err != nil {
		if errors.Is(err, swarm.ErrServiceNotFound) {
			return nil, withStatusError(
				http.StatusNotFound,
				fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
			)
		}

		slog.ErrorContext(
			ctx,
			"[webserver] failed to read service status for deployments",
			slog.String("stack", params.Stack),
			slog.String("service", params.Service),
			slog.Any("err", err),
		)
		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get service deployments"))
	}

	items := []generated.ServiceDeploymentResponse{}
	if h.history != nil {
		limit := serviceDeploymentsLimit
		if value, ok := params.Limit.Get(); ok {
			limit = int(value)
		}

		items = toGeneratedServiceDeployments(
			h.history.List(),
			params.Stack,
			params.Service,
			status.Spec.Image,
			limit,
		)
	}

	return &generated.ServiceDeploymentsResponse{
		Deployments: items,
	}, nil
}
