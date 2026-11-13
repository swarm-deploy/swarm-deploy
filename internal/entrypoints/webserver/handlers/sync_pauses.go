package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
)

// GetStackSyncPauses returns the active stack pause and calculated effective count.
func (h *handler) GetStackSyncPauses(
	ctx context.Context, params generated.GetStackSyncPausesParams,
) (generated.GetStackSyncPausesRes, error) {
	state, err := h.syncPauses.GetStack(ctx, params.Stack)
	if errors.Is(err, syncpause.ErrStackNotManaged) {
		return &generated.GetStackSyncPausesNotFound{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := toGeneratedStackSyncPauses(state)
	return &result, nil
}

// PutStackSyncPause creates or changes the stack pause.
func (h *handler) PutStackSyncPause(
	ctx context.Context, request *generated.SyncPauseRequest, params generated.PutStackSyncPauseParams,
) (*generated.StackSyncPausesResponse, error) {
	if _, err := h.syncPauses.SetStack(ctx, params.Stack, requestExpiration(request)); err != nil {
		return nil, pauseHTTPError(err)
	}
	state, err := h.syncPauses.GetStack(ctx, params.Stack)
	if err != nil {
		return nil, pauseHTTPError(err)
	}
	result := toGeneratedStackSyncPauses(state)
	return &result, nil
}

// DeleteStackSyncPause removes the stack pause.
func (h *handler) DeleteStackSyncPause(
	ctx context.Context, params generated.DeleteStackSyncPauseParams,
) error {
	_, err := h.syncPauses.ResumeStack(ctx, params.Stack)
	return pauseHTTPError(err)
}

func requestExpiration(request *generated.SyncPauseRequest) *time.Time {
	if request == nil || request.ExpiresAt.IsNull() {
		return nil
	}
	value, _ := request.ExpiresAt.Get()
	return &value
}

func toGeneratedStackSyncPauses(state syncpause.StackState) generated.StackSyncPausesResponse {
	result := generated.StackSyncPausesResponse{
		EffectivelyPausedServices: len(state.EffectivelyPausedServices),
	}
	if state.StackPause != nil {
		result.StackPause = generated.NewOptSyncPauseRecord(toGeneratedSyncPause(*state.StackPause))
	}
	return result
}

func toGeneratedSyncPause(pause syncpause.Pause) generated.SyncPauseRecord {
	expires := generated.NilDateTime{}
	if pause.ExpiresAt == nil {
		expires.SetToNull()
	} else {
		expires.SetTo(pause.ExpiresAt.UTC())
	}
	return generated.SyncPauseRecord{Stack: pause.StackName, ExpiresAt: expires}
}

func pauseHTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syncpause.ErrStackNotManaged):
		return withStatusError(http.StatusNotFound, err)
	case errors.Is(err, syncpause.ErrExpirationNotFuture):
		return withStatusError(http.StatusBadRequest, err)
	default:
		return err
	}
}
