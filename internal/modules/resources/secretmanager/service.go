package secretmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager/cloudsecrets"
)

var (
	// ErrNotFound means that the requested Secret Manager was not discovered.
	ErrNotFound = errors.New("secret manager not found")
	// ErrNotControllable means that the requested Secret Manager has no supported controller endpoint.
	ErrNotControllable = errors.New("secret manager is not controllable")
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
func (s *Service) List(ctx context.Context) []Info {
	targets := s.resolver.resolve()
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

	return managers
}

// Sync triggers synchronization for a discovered controllable Secret Manager.
func (s *Service) Sync(ctx context.Context, stack string, service string) (SyncResult, error) {
	resolvedTarget, ok := s.resolver.find(stack, service)
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
