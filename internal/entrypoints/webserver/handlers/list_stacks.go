package handlers

import (
	"context"
	"strings"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
)

func (h *handler) ListStacks(ctx context.Context) (*generated.StacksResponse, error) {
	state, err := h.stateStore.Read(ctx)
	if err != nil {
		return nil, err
	}
	syncInfo := lastSyncInfo(state)

	return &generated.StacksResponse{
		Stacks: h.listStacks(state),
		Sync:   syncInfo,
	}, nil
}

func lastSyncInfo(state model.Runtime) map[string]string {
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
