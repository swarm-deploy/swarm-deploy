package modelstore

import "github.com/swarm-deploy/swarm-deploy/internal/gitops/model"

type Store interface {
	ReadStore

	// Update applies mutation to runtime state.
	Update(fn func(*model.Runtime))

	Stop()
}

type ReadStore interface {
	// Get returns a snapshot copy of current runtime state.
	Get() model.Runtime
}
