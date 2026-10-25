package secretmanager

import (
	"context"

	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
)

// Domain owns Secret Manager discovery and controller operations.
type Domain struct {
	service *Service
}

// NewDomain initializes Secret Manager discovery from persisted service resources.
func NewDomain(services servicestore.Store) *Domain {
	return &Domain{
		service: NewService(NewResolver(services)),
	}
}

// List returns discovered Secret Managers without failing when a controller is unavailable.
func (d *Domain) List(ctx context.Context) ([]Info, error) {
	return d.service.List(ctx)
}

// Sync triggers synchronization for a discovered controllable Secret Manager.
func (d *Domain) Sync(ctx context.Context, stack string, service string) (SyncResult, error) {
	return d.service.Sync(ctx, stack, service)
}
