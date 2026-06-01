package statem

import (
	"sync"
)

type MemoryStore struct {
	mu    sync.RWMutex
	state Runtime
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		state: Runtime{
			Stacks:   map[string]Stack{},
			Networks: map[string]Network{},
		},
	}
}

func (s *MemoryStore) Get() Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return cloneRuntime(s.state)
}

func (s *MemoryStore) Stop() {}

func (s *MemoryStore) Update(fn func(*Runtime)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.state)
}
