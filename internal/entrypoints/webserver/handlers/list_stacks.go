package handlers

import (
	"context"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListStacks(_ context.Context) (*generated.StacksResponse, error) {
	syncInfo := h.control.LastSyncInfo()

	return &generated.StacksResponse{
		Stacks: toGeneratedStacks(h.control.ListStacks()),
		Sync:   syncInfo,
	}, nil
}
