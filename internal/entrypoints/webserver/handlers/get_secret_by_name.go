package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) GetSecretByName(
	ctx context.Context,
	params generated.GetSecretByNameParams,
) (*generated.SecretDetailsResponse, error) {
	if h.secrets == nil {
		return nil, withStatusError(http.StatusNotFound, fmt.Errorf("secret %q not found", params.Name))
	}

	secrets, err := h.secrets.List(ctx)
	if err != nil {
		return nil, withStatusError(http.StatusInternalServerError, fmt.Errorf("list docker secrets: %w", err))
	}

	for _, secret := range secrets {
		if secret.Name != params.Name {
			continue
		}

		resp := &generated.SecretDetailsResponse{
			ID:        secret.ID,
			Name:      secret.Name,
			VersionID: toInt64FromUint64(secret.VersionID),
			CreatedAt: secret.CreatedAt,
			UpdatedAt: secret.UpdatedAt,
			External:  toGeneratedSecretExternal(secret.Labels),
		}
		if driver := strings.TrimSpace(secret.Driver); driver != "" {
			resp.Driver = generated.NewOptString(driver)
		}
		if len(secret.Labels) > 0 {
			labels := make(generated.SecretDetailsResponseLabels, len(secret.Labels))
			for key, value := range secret.Labels {
				labels[key] = value
			}
			resp.Labels = generated.NewOptSecretDetailsResponseLabels(labels)
		}

		return resp, nil
	}

	return nil, withStatusError(http.StatusNotFound, fmt.Errorf("secret %q not found", params.Name))
}
