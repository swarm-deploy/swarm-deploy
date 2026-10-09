package handlers

import (
	"context"
	"fmt"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) GetService(
	ctx context.Context,
	params generated.GetServiceParams,
) (*generated.ServiceStatusResponse, error) {
	info, ok, err := h.services.Find(ctx, params.Stack, params.Service)
	if err != nil {
		return nil, err
	}
	if ok {
		return toGeneratedServiceStatusFromInfo(info), nil
	}

	return nil, withStatusError(
		http.StatusNotFound,
		fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
	)
}
