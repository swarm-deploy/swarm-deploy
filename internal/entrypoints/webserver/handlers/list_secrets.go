package handlers

import (
	"context"
	"fmt"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListSecrets(ctx context.Context) (*generated.SecretsResponse, error) {
	secrets, err := h.secretRelations.ListSecretsWithUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	return &generated.SecretsResponse{
		Secrets: toGeneratedSecrets(secrets),
	}, nil
}
