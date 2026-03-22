package handlers

import (
	"context"

	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListStacks(_ context.Context) (*generated.StacksResponse, error) {
	return &generated.StacksResponse{
		Stacks: toGeneratedStacks(h.control.ListStacks()),
		Sync:   h.control.LastSyncInfo(),
	}, nil
}
