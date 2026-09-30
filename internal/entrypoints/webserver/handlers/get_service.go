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
	info, ok := h.services.Get(params.Stack, params.Service)
	if ok {
		resolvedSecrets, err := h.secretRelations.ResolveServiceSecrets(ctx, info)
		if err != nil {
			return nil, withStatusError(
				http.StatusInternalServerError,
				fmt.Errorf("resolve service secrets: %w", err),
			)
		}

		return toGeneratedServiceStatusFromInfo(info, resolvedSecrets), nil
	}

	return nil, withStatusError(
		http.StatusNotFound,
		fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
	)
}
