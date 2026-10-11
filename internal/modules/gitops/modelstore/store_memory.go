package modelstore

import (
	"context"
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

type MemoryStore struct {
	mu    sync.RWMutex
	state model.Runtime
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		state: model.Runtime{
			Stacks:   map[string]model.Stack{},
			Networks: map[string]model.Network{},
		},
	}
}

func (s *MemoryStore) Get() model.Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.state.Clone()
}

func (s *MemoryStore) Stop() {}

func (s *MemoryStore) Update(_ context.Context, fn func(*model.Runtime)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.state)
	return nil
}

// Read returns the test/in-memory snapshot through the repository contract.
func (s *MemoryStore) Read(_ context.Context) (model.Runtime, error) { return s.Get(), nil }
