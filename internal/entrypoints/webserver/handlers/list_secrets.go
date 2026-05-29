package handlers

import (
	"context"
	"fmt"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListSecrets(ctx context.Context) (*generated.SecretsResponse, error) {
	items := []generated.SecretInfo{}
	if h.secrets != nil {
		secrets, err := h.secrets.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list docker secrets: %w", err)
		}
		items = toGeneratedSecrets(secrets)
	}

	return &generated.SecretsResponse{
		Secrets: items,
	}, nil
}
