package handlers

import (
	"context"
	"strings"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListStacks(_ context.Context) (*generated.StacksResponse, error) {
	syncInfo := h.lastSyncInfo()

	return &generated.StacksResponse{
		Stacks: h.listStacks(),
		Sync:   syncInfo,
	}, nil
}

func (h *handler) lastSyncInfo() map[string]string {
	state := h.stateStore.Get()
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
