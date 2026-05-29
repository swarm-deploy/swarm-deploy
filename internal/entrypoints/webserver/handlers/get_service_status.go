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

func (h *handler) GetServiceStatus(
	ctx context.Context,
	params generated.GetServiceStatusParams,
) (*generated.ServiceStatusResponse, error) {
	status, err := h.serviceInspector.GetStatus(ctx, swarm.NewServiceReference(params.Stack, params.Service))
	if err == nil {
		return toGeneratedServiceStatus(status), nil
	}

	if errors.Is(err, swarm.ErrServiceNotFound) {
		return nil, withStatusError(
			http.StatusNotFound,
			fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
		)
	}

	slog.ErrorContext(
		ctx,
		"[webserver] failed to read service status",
		slog.String("stack", params.Stack),
		slog.String("service", params.Service),
		slog.Any("err", err),
	)
	return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to get service status"))
}
