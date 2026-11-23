package handlers

import (
	"context"
	"fmt"
	"log/slog"
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
		resolvedSecrets, resolveErr := h.secretRelations.ResolveServiceSecrets(ctx, info)
		if resolveErr != nil {
			slog.ErrorContext(
				ctx,
				"failed to resolve service secrets",
				slog.String("stack", params.Stack),
				slog.String("service", params.Service),
				slog.Any("error", resolveErr),
			)
		}

		return toGeneratedServiceStatusFromInfo(info, resolvedSecrets), nil
	}

	return nil, withStatusError(
		http.StatusNotFound,
		fmt.Errorf("service %s/%s not found", params.Stack, params.Service),
	)
}
