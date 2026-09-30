package handlers

import (
	"context"
	"errors"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
)

// ListSecretManagers returns discovered Secret Managers and controller availability.
func (h *handler) ListSecretManagers(ctx context.Context) (*generated.SecretManagersResponse, error) {
	managers := h.secretManagers.List(ctx)
	mapped := make([]generated.SecretManagerInfo, 0, len(managers))
	for _, manager := range managers {
		item := generated.SecretManagerInfo{
			Stack:        manager.Stack,
			Service:      manager.Service,
			Kind:         manager.Kind,
			Controllable: manager.Controllable,
			Available:    manager.Available,
			Version:      toOptString(manager.Version),
			Error:        toOptString(manager.Error),
		}
		if manager.Provider.Name != "" || manager.Provider.Link != "" {
			item.Provider = generated.NewOptSecretManagerProvider(generated.SecretManagerProvider{
				Name: manager.Provider.Name,
				Link: toOptString(manager.Provider.Link),
			})
		}
		if manager.LastSyncAt != nil {
			item.LastSyncAt = generated.NewOptDateTime(*manager.LastSyncAt)
		}

		mapped = append(mapped, item)
	}

	return &generated.SecretManagersResponse{SecretManagers: mapped}, nil
}

// SyncSecretManager triggers a manual Secret Manager synchronization.
func (h *handler) SyncSecretManager(
	ctx context.Context,
	params generated.SyncSecretManagerParams,
) (*generated.SecretManagerSyncResponse, error) {
	result, err := h.secretManagers.Sync(ctx, params.Stack, params.Service)
	if err != nil {
		switch {
		case errors.Is(err, secretmanager.ErrNotFound):
			return nil, withStatusError(http.StatusNotFound, err)
		case errors.Is(err, secretmanager.ErrNotControllable):
			return nil, withStatusError(http.StatusConflict, err)
		default:
			return nil, withStatusError(http.StatusServiceUnavailable, err)
		}
	}

	return &generated.SecretManagerSyncResponse{
		Created:   int64(result.Created),
		Updated:   int64(result.Updated),
		Removed:   int64(result.Removed),
		Unchanged: int64(result.Unchanged),
	}, nil
}
