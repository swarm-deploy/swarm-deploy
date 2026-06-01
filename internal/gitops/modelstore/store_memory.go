package modelstore

import (
	"sync"

	"github.com/swarm-deploy/swarm-deploy/internal/gitops/model"
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

func (s *MemoryStore) Update(fn func(*model.Runtime)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.state)
}
