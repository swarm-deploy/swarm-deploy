package modelstore

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

type Store interface {
	ReadStore

	// Update applies mutation to runtime state.
	Update(ctx context.Context, fn func(*model.Runtime)) error
}

type ReadStore interface {
	// Get returns a snapshot copy of current runtime state.
	Read(ctx context.Context) (model.Runtime, error)
}
