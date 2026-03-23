package handlers

import (
	"context"
	"strconv"

	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
)

func (h *handler) ListStacks(_ context.Context) (*generated.StacksResponse, error) {
	syncInfo := h.control.LastSyncInfo()
	if syncInfo == nil {
		syncInfo = map[string]string{}
	}
	syncInfo["assistant_enabled"] = strconv.FormatBool(h.assistantEnabled)

	return &generated.StacksResponse{
		Stacks: toGeneratedStacks(h.control.ListStacks()),
		Sync:   syncInfo,
	}, nil
}
