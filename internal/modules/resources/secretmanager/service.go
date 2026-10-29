package secretmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager/cloudsecrets"
)

var (
	// ErrNotFound means that the requested Secret Manager was not discovered.
	ErrNotFound = errors.New("secret manager not found")
	// ErrNotControllable means that the requested Secret Manager has no supported controller endpoint.
	ErrNotControllable = errors.New("secret manager is not controllable")
	// ErrUnavailable means that the selected Secret Manager controller cannot be reached.
	ErrUnavailable = errors.New("secret manager is unavailable")
	// ErrUnsupportedProvider means that the selected cloud-secrets provider cannot create secrets through this API.
	ErrUnsupportedProvider = errors.New("secret manager provider is unsupported")
	// ErrInvalidCreateRequest means that the request does not match the selected provider contract.
	ErrInvalidCreateRequest = errors.New("invalid external secret creation request")
)

type cloudSecretsClientFactory func(address string) (cloudsecrets.Controller, error)

// Service probes discovered Secret Managers and invokes their controllers.
type Service struct {
	resolver                  *Resolver
	newCloudSecretsController cloudSecretsClientFactory
}

// NewService creates a Secret Manager service.
func NewService(resolver *Resolver) *Service {
	return &Service{
		resolver:                  resolver,
		newCloudSecretsController: cloudsecrets.NewClient,
	}
}

// List returns discovered Secret Managers without failing when a controller is unavailable.
func (s *Service) List(ctx context.Context) ([]Info, error) {
	targets, readErr := s.resolver.resolve(ctx)
	if readErr != nil {
		return nil, readErr
	}
	managers := make([]Info, 0, len(targets))
	for _, target := range targets {
		manager := Info{
			Stack:        target.stack,
			Service:      target.service,
			Kind:         target.kind,
			Controllable: target.controllable,
		}
		if !target.controllable {
			managers = append(managers, manager)
			continue
		}
		if target.discoveryError != nil {
			manager.Error = target.discoveryError.Error()
			managers = append(managers, manager)
			continue
		}

		controller, err := s.newCloudSecretsController(target.address)
		if err != nil {
			manager.Error = fmt.Sprintf("create controller client: %v", err)
			managers = append(managers, manager)
			continue
		}
		info, infoErr := controller.GetInfo(ctx)
		closeErr := controller.Close()
		if infoErr != nil {
			manager.Error = fmt.Sprintf("get controller info: %v", infoErr)
			managers = append(managers, manager)
			continue
		}
		if closeErr != nil {
			manager.Error = fmt.Sprintf("close controller client: %v", closeErr)
			managers = append(managers, manager)
			continue
		}

		manager.Available = true
		manager.Version = info.Version
		manager.Provider = Provider{
			Name: info.ProviderName,
			Links: ProviderLinks{
				Doc:     info.ProviderLinks.Doc,
				Manager: info.ProviderLinks.Manager,
			},
		}
		manager.LastSyncAt = info.LastSyncAt
		manager.NextSyncAt = info.NextSyncAt
		managers = append(managers, manager)
	}

	return managers, nil
}

// Sync triggers synchronization for a discovered controllable Secret Manager.
func (s *Service) Sync(ctx context.Context, stack string, service string) (SyncResult, error) {
	resolvedTarget, ok, resolveErr := s.resolver.find(ctx, stack, service)
	if resolveErr != nil {
		return SyncResult{}, resolveErr
	}
	if !ok {
		return SyncResult{}, ErrNotFound
	}
	if !resolvedTarget.controllable || resolvedTarget.kind != cloudSecretsKind {
		return SyncResult{}, ErrNotControllable
	}
	if resolvedTarget.discoveryError != nil {
		return SyncResult{}, fmt.Errorf("discover controller: %w", resolvedTarget.discoveryError)
	}

	controller, err := s.newCloudSecretsController(resolvedTarget.address)
	if err != nil {
		return SyncResult{}, fmt.Errorf("create controller client: %w", err)
	}
	defer controller.Close()

	result, err := controller.Sync(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync cloud-secrets: %w", err)
	}

	return SyncResult{
		Created:   result.Created,
		Updated:   result.Updated,
		Removed:   result.Removed,
		Unchanged: result.Unchanged,
	}, nil
}

// Create creates one secret in a discovered cloud-secrets provider without synchronizing it.
func (s *Service) Create(
	ctx context.Context,
	stack string,
	service string,
	req CreateRequest,
) (CreateResult, error) {
	resolvedTarget, ok, resolveErr := s.resolver.find(ctx, stack, service)
	if resolveErr != nil {
		return CreateResult{}, resolveErr
	}
	if !ok {
		return CreateResult{}, ErrNotFound
	}
	if !resolvedTarget.controllable || resolvedTarget.kind != cloudSecretsKind {
		return CreateResult{}, ErrNotControllable
	}
	if resolvedTarget.discoveryError != nil {
		return CreateResult{}, fmt.Errorf("%w: discover controller: %v", ErrUnavailable, resolvedTarget.discoveryError)
	}
	if strings.TrimSpace(req.Path) == "" {
		return CreateResult{}, fmt.Errorf("%w: path is required", ErrInvalidCreateRequest)
	}
	if len(req.Value) == 0 {
		return CreateResult{}, fmt.Errorf("%w: value is required", ErrInvalidCreateRequest)
	}

	controller, err := s.newCloudSecretsController(resolvedTarget.address)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: create controller client: %v", ErrUnavailable, err)
	}
	defer controller.Close()

	info, err := controller.GetInfo(ctx)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: get controller info: %v", ErrUnavailable, err)
	}
	switch info.ProviderName {
	case providerHashiCorpVault:
		if strings.TrimSpace(req.Key) == "" {
			return CreateResult{}, fmt.Errorf("%w: key is required for Vault KV v2", ErrInvalidCreateRequest)
		}
		if req.Description != "" {
			return CreateResult{}, fmt.Errorf("%w: description is not supported by Vault KV v2", ErrInvalidCreateRequest)
		}
	case providerCloudRu:
		if strings.TrimSpace(req.Key) != "" {
			return CreateResult{}, fmt.Errorf("%w: key must be empty for Cloud.ru", ErrInvalidCreateRequest)
		}
	default:
		return CreateResult{}, fmt.Errorf("%w: %q", ErrUnsupportedProvider, info.ProviderName)
	}

	result, err := controller.Create(ctx, cloudsecrets.CreateRequest{
		Path:        req.Path,
		Value:       req.Value,
		Key:         req.Key,
		Description: req.Description,
	})
	if err != nil {
		return CreateResult{}, fmt.Errorf("create cloud-secrets secret: %w", err)
	}

	return CreateResult{Path: result.Path}, nil
}
