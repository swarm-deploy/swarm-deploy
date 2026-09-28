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
		"last_poll_result": state.LastPollResult,
		"last_poll_error":  strings.TrimSpace(state.LastPollError),
		"last_sync_reason": state.LastSyncReason,
		"last_sync_result": state.LastSyncResult,
		"last_sync_error":  strings.TrimSpace(state.LastSyncError),
		"git_revision":     state.GitRevision,
	}
	if !state.LastPollAt.IsZero() {
		info["last_poll_at"] = state.LastPollAt.Format(time.RFC3339)
	}
	if !state.LastSyncAt.IsZero() {
		info["last_sync_at"] = state.LastSyncAt.Format(time.RFC3339)
	}

	return info
}
