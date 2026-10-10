package modelstore

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
)

// Store persists and queries service metadata snapshots.
type Store interface {
	// List returns a copy of all saved services.
	ReadAll(ctx context.Context) ([]model.Info, error)
	// Get returns saved service metadata by stack and service names.
	Find(ctx context.Context, stackName string, serviceName string) (model.Info, bool, error)
	// ReplaceStack replaces stack services with a new snapshot and saves it to disk.
	ReplaceStack(ctx context.Context, stackName string, services []model.Info) error
}
