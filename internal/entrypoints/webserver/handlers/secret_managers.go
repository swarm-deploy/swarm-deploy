package handlers

import (
	"context"
	"errors"
	"net/http"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListSecretManagers returns discovered Secret Managers and controller availability.
func (h *handler) ListSecretManagers(ctx context.Context) (*generated.SecretManagersResponse, error) {
	managers, err := h.secretManagers.List(ctx)
	if err != nil {
		return nil, err
	}
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
		if manager.Provider.Name != "" || manager.Provider.Links.Doc != "" || manager.Provider.Links.Manager != "" {
			provider := generated.SecretManagerProvider{Name: manager.Provider.Name}
			if manager.Provider.Links.Doc != "" || manager.Provider.Links.Manager != "" {
				provider.Links = generated.NewOptSecretManagerProviderLinks(generated.SecretManagerProviderLinks{
					Doc:     toOptString(manager.Provider.Links.Doc),
					Manager: toOptString(manager.Provider.Links.Manager),
				})
			}
			item.Provider = generated.NewOptSecretManagerProvider(provider)
		}
		if manager.LastSyncAt != nil {
			item.LastSyncAt = generated.NewOptDateTime(*manager.LastSyncAt)
		}
		if manager.NextSyncAt != nil {
			item.NextSyncAt = generated.NewOptDateTime(*manager.NextSyncAt)
		}

		mapped = append(mapped, item)
	}

	return &generated.SecretManagersResponse{SecretManagers: mapped}, nil
}

// CreateExternalSecret creates a secret in the selected external provider without synchronizing it.
func (h *handler) CreateExternalSecret(
	ctx context.Context,
	req *generated.CreateExternalSecretRequest,
	params generated.CreateExternalSecretParams,
) (*generated.CreateExternalSecretResponse, error) {
	key, _ := req.Key.Get()
	description, _ := req.Description.Get()
	result, err := h.secretManagers.Create(ctx, params.Stack, params.Service, secretmanager.CreateRequest{
		Path:        req.Path,
		Key:         key,
		Value:       []byte(req.Value),
		Description: description,
	})
	if err != nil {
		return nil, externalSecretCreateError(err)
	}

	return &generated.CreateExternalSecretResponse{Path: result.Path}, nil
}

func externalSecretCreateError(err error) error {
	switch {
	case errors.Is(err, secretmanager.ErrNotFound):
		return withStatusError(http.StatusNotFound, err)
	case errors.Is(err, secretmanager.ErrNotControllable):
		return withStatusError(http.StatusConflict, err)
	case errors.Is(err, secretmanager.ErrInvalidCreateRequest),
		errors.Is(err, secretmanager.ErrUnsupportedProvider),
		status.Code(err) == codes.InvalidArgument,
		status.Code(err) == codes.FailedPrecondition:
		return withStatusError(http.StatusBadRequest, err)
	case status.Code(err) == codes.AlreadyExists, status.Code(err) == codes.Aborted:
		return withStatusError(http.StatusConflict, err)
	case status.Code(err) == codes.DeadlineExceeded:
		return withStatusError(http.StatusGatewayTimeout, err)
	case errors.Is(err, secretmanager.ErrUnavailable), status.Code(err) == codes.Unavailable:
		return withStatusError(http.StatusServiceUnavailable, err)
	default:
		return withStatusError(http.StatusBadGateway, err)
	}
}

// SyncSecretManager triggers a manual Secret Manager synchronization.
func (h *handler) SyncSecretManager(
	ctx context.Context,
	params generated.SyncSecretManagerParams,
) (*generated.SecretManagerSyncResponse, error) {
	result, err := h.secretManagers.Sync(ctx, params.Stack, params.Service)
	if err != nil {
		return nil, secretManagerSyncError(err)
	}

	return &generated.SecretManagerSyncResponse{
		Created:   int64(result.Created),
		Updated:   int64(result.Updated),
		Removed:   int64(result.Removed),
		Unchanged: int64(result.Unchanged),
	}, nil
}

func secretManagerSyncError(err error) error {
	switch {
	case errors.Is(err, secretmanager.ErrNotFound):
		return withStatusError(http.StatusNotFound, err)
	case errors.Is(err, secretmanager.ErrNotControllable), status.Code(err) == codes.Aborted:
		return withStatusError(http.StatusConflict, err)
	case status.Code(err) == codes.DeadlineExceeded:
		return withStatusError(http.StatusGatewayTimeout, err)
	default:
		return withStatusError(http.StatusServiceUnavailable, err)
	}
}
