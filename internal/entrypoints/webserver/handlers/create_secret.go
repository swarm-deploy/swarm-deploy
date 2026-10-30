package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/containerd/errdefs"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

var dockerSecretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// CreateSecret creates a standalone Docker Swarm secret.
func (h *handler) CreateSecret(
	ctx context.Context,
	req *generated.CreateSecretRequest,
) (*generated.CreateSecretResponse, error) {
	if !dockerSecretNamePattern.MatchString(req.Name) {
		return nil, withStatusError(
			http.StatusBadRequest,
			errors.New("secret name must be 1-64 characters and contain only letters, numbers, dots, dashes, or underscores"),
		)
	}
	if req.Value == "" {
		return nil, withStatusError(http.StatusBadRequest, errors.New("secret value is required"))
	}

	labels := map[string]string(nil)
	if description, ok := req.Description.Get(); ok && strings.TrimSpace(description) != "" {
		labels = map[string]string{"description": strings.TrimSpace(description)}
	}
	id, err := h.dockerSecrets.Create(ctx, swarm.CreateSecretRequest{
		Name:   req.Name,
		Data:   []byte(req.Value),
		Labels: labels,
	})
	if err != nil {
		if errdefs.IsConflict(err) || errdefs.IsAlreadyExists(err) {
			return nil, withStatusError(http.StatusConflict, errors.New("a Docker secret with this name already exists"))
		}

		return nil, withStatusError(http.StatusInternalServerError, errors.New("unable to create Docker secret"))
	}

	return &generated.CreateSecretResponse{ID: id, Name: req.Name}, nil
}
