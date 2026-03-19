package controller

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
)

type serviceState struct {
	Image        string
	LastStatus   string
	LastDeployAt time.Time
}

type stackState struct {
	SourceDigest string
	LastCommit   string
	LastStatus   string
	LastError    string
	LastDeployAt time.Time
	Services     map[string]serviceState
}

type runtimeState struct {
	LastSyncAt     time.Time
	LastSyncReason string
	LastSyncResult string
	LastSyncError  string
	GitRevision    string
	Stacks         map[string]stackState
}

type runtimeStateStore struct {
	mu    sync.RWMutex
	state runtimeState
}

func newRuntimeStateStore() *runtimeStateStore {
	return &runtimeStateStore{
		state: runtimeState{
			Stacks: map[string]stackState{},
		},
	}
}

func (s *runtimeStateStore) snapshot() runtimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := s.state
	cloned.Stacks = map[string]stackState{}
	for stackName, st := range s.state.Stacks {
		stackCopy := st
		stackCopy.Services = map[string]serviceState{}
		for serviceName, service := range st.Services {
			stackCopy.Services[serviceName] = service
		}
		cloned.Stacks[stackName] = stackCopy
	}

	return cloned
}

func (s *runtimeStateStore) update(fn func(*runtimeState)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.state)
	if s.state.Stacks == nil {
		s.state.Stacks = map[string]stackState{}
	}
}

func (s *runtimeStateStore) listStacks(specs []config.StackSpec) []StackView {
	snapshot := s.snapshot()
	stacks := make([]StackView, 0, len(specs))

	for _, stackCfg := range specs {
		stackSnapshot, exists := snapshot.Stacks[stackCfg.Name]
		view := StackView{
			Name:         stackCfg.Name,
			ComposeFile:  stackCfg.ComposeFile,
			LastStatus:   "unknown",
			SourceDigest: stackSnapshot.SourceDigest,
			Services:     nil,
		}
		if exists {
			view.LastStatus = stackSnapshot.LastStatus
			view.LastError = stackSnapshot.LastError
			view.LastCommit = stackSnapshot.LastCommit
			view.LastDeployAt = stackSnapshot.LastDeployAt
		}

		serviceNames := make([]string, 0, len(stackSnapshot.Services))
		for serviceName := range stackSnapshot.Services {
			serviceNames = append(serviceNames, serviceName)
		}
		sort.Strings(serviceNames)

		for _, serviceName := range serviceNames {
			service := stackSnapshot.Services[serviceName]
			view.Services = append(view.Services, ServiceView{
				Name:         serviceName,
				Image:        service.Image,
				ImageVersion: compose.ImageVersion(service.Image),
				LastStatus:   service.LastStatus,
				LastDeployAt: service.LastDeployAt,
			})
		}

		stacks = append(stacks, view)
	}

	return stacks
}

func (s *runtimeStateStore) lastSyncInfo() map[string]string {
	state := s.snapshot()
	info := map[string]string{
		"last_sync_reason": state.LastSyncReason,
		"last_sync_result": state.LastSyncResult,
		"last_sync_error":  strings.TrimSpace(state.LastSyncError),
		"git_revision":     state.GitRevision,
	}
	if !state.LastSyncAt.IsZero() {
		info["last_sync_at"] = state.LastSyncAt.Format(time.RFC3339)
	}
	return info
}

func (c *Controller) ListStacks() []StackView {
	return c.stateStore.listStacks(c.cfg.Spec.Stacks)
}

func (c *Controller) LastSyncInfo() map[string]string {
	return c.stateStore.lastSyncInfo()
}

func (c *Controller) snapshotState() runtimeState {
	return c.stateStore.snapshot()
}

func (c *Controller) updateState(fn func(*runtimeState)) {
	c.stateStore.update(fn)
}
