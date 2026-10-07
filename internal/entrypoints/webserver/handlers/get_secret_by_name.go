package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
)

func (h *handler) GetSecretByName(
	ctx context.Context,
	params generated.GetSecretByNameParams,
) (*generated.SecretDetailsResponse, error) {
	secret, err := h.secrets.GetByName(ctx, params.Name)
	if err != nil {
		if errors.Is(err, modelstore.ErrSecretNotFound) {
			return nil, withStatusError(http.StatusNotFound, fmt.Errorf("secret %q not found", params.Name))
		}

		return nil, withStatusError(http.StatusInternalServerError, fmt.Errorf("get secret: %w", err))
	}

	resp := &generated.SecretDetailsResponse{
		ID:        secret.ID,
		Name:      secret.Name,
		VersionID: toInt64FromUint64(secret.VersionID),
		CreatedAt: secret.CreatedAt,
		UpdatedAt: secret.UpdatedAt,
		External:  toGeneratedSecretExternal(secret.ExternalPath, secret.ExternalVersionID),
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
