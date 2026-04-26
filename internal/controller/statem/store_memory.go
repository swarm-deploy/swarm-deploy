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
			Stacks: map[string]Stack{},
		},
	}
}

func (s *MemoryStore) Get() Runtime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := s.state
	cloned.Stacks = map[string]Stack{}
	for stackName, st := range s.state.Stacks {
		stackCopy := st
		stackCopy.Services = map[string]Service{}
		for serviceName, service := range st.Services {
			stackCopy.Services[serviceName] = service
		}
		cloned.Stacks[stackName] = stackCopy
	}

	return cloned
}

func (s *MemoryStore) Update(fn func(*Runtime)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.state)
	if s.state.Stacks == nil {
		s.state.Stacks = map[string]Stack{}
	}
}
