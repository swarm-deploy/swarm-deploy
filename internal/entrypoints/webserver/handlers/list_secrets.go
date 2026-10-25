package handlers

import (
	"context"
	"fmt"
	"log/slog"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListSecrets(ctx context.Context) (*generated.SecretsResponse, error) {
	secrets, err := h.secretRelations.ListSecretsWithUsage(ctx)
	if err != nil {
		if secrets == nil {
			return nil, fmt.Errorf("list secrets: %w", err)
		}

		slog.ErrorContext(ctx, "failed to resolve services using secrets", slog.Any("error", err))
	}

	return &generated.SecretsResponse{
		Secrets: toGeneratedSecrets(secrets),
	}, nil
}
