package secrets

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/secretmanager"
	resourceservice "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Domain owns persisted secret metadata and its Docker collector.
type Domain struct {
	// Store persists and queries the current secret metadata snapshot.
	Store modelstore.Store
	// Collector refreshes the snapshot from Docker.
	Collector *Collector
	// Managers discovers and controls Secret Manager services.
	Managers *secretmanager.Service
}

// NewDomain initializes the secret metadata domain.
func NewDomain(
	ctx context.Context,
	dataDir string,
	filesystem fs.FileSystem,
	secretManager swarm.SecretManager,
	serviceStore *resourceservice.Store,
) (*Domain, error) {
	store, err := modelstore.NewFileStore(ctx, filepath.Join(dataDir, "secrets.state.json"), filesystem)
	if err != nil {
		return nil, fmt.Errorf("init secret store: %w", err)
	}

	return &Domain{
		Store:     store,
		Collector: NewCollector(secretManager, store),
		Managers:  secretmanager.NewService(secretmanager.NewResolver(serviceStore)),
	}, nil
}
