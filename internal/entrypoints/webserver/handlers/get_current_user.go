package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

func (h *handler) GetCurrentUser(ctx context.Context) (*generated.CurrentUserResponse, error) {
	user, ok := security.UserFromContext(ctx)
	if !ok {
		return nil, withStatusError(http.StatusUnauthorized, errors.New("user is not authenticated"))
	}

	name := strings.TrimSpace(user.Name)
	if name == "" {
		return nil, withStatusError(http.StatusUnauthorized, errors.New("user name is empty"))
	}

	return &generated.CurrentUserResponse{
		Name: name,
	}, nil
}
