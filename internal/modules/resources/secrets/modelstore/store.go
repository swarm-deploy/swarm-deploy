package modelstore

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
)

// ErrSecretNotFound indicates that a secret name is absent from the snapshot.
var ErrSecretNotFound = errors.New("secret not found")

// Store persists and queries a secret metadata snapshot.
type Store interface {
	// List returns all secrets sorted by name and identifier.
	List(ctx context.Context) ([]model.Secret, error)
	// GetByID returns a secret by its Docker identifier.
	GetByID(ctx context.Context, id string) (model.Secret, error)
	// GetByName returns a secret by its Docker name.
	GetByName(ctx context.Context, name string) (model.Secret, error)
	// Replace atomically replaces the complete metadata snapshot.
	Replace(ctx context.Context, secrets []model.Secret) error
}
