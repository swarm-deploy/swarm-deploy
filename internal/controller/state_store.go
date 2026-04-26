package controller

import (
	"sort"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/controller/statem"
)

func newRuntimeStateStore() *statem.MemoryStore {
	return statem.NewMemoryStore()
}

func (c *Controller) ListStacks() []StackView {
	snapshot := c.stateStore.Get()
	stacks := make([]StackView, 0, len(c.cfg.Spec.Stacks))

	for _, stackCfg := range c.cfg.Spec.Stacks {
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

func (c *Controller) LastSyncInfo() map[string]string {
	state := c.snapshotState()
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

func (c *Controller) snapshotState() statem.Runtime {
	return c.stateStore.Get()
}

func (c *Controller) updateState(fn func(*statem.Runtime)) {
	c.stateStore.Update(fn)
}
