package handlers

import (
	"context"
	"fmt"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) GetService(
	_ context.Context,
	params generated.GetServiceParams,
) (*generated.ServiceStatusResponse, error) {
	info, ok := h.services.Get(params.Stack, params.Service)
	if ok {
		return toGeneratedServiceStatusFromInfo(info), nil
	}

	return nil, withStatusError(
		http.StatusNotFound,
		fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
	)
}
