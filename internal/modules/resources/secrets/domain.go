package secrets

import (
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// Domain owns persisted secret metadata and its Docker collector.
type Domain struct {
	// Store persists and queries the current secret metadata snapshot.
	Store modelstore.Store
	// Collector refreshes the snapshot from Docker.
	Collector *Collector
}

// NewDomain initializes the secret metadata domain.
func NewDomain(
	db *storage.Database,
	secretManager swarm.SecretManager,
) (*Domain, error) {
	store := modelstore.NewSQLStore(db)

	return &Domain{
		Store:     store,
		Collector: NewCollector(secretManager, store),
	}, nil
}
