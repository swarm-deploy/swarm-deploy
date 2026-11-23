package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
)

// GetStackSyncPauses returns active direct pauses and the calculated effective count.
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

// PutStackSyncPause creates or changes the direct stack pause.
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

// DeleteStackSyncPause removes only the direct stack pause.
func (h *handler) DeleteStackSyncPause(
	ctx context.Context, params generated.DeleteStackSyncPauseParams,
) error {
	_, err := h.syncPauses.ResumeStack(ctx, params.Stack)
	return pauseHTTPError(err)
}

// GetServiceSyncPause returns direct and effective pause state for a managed service.
func (h *handler) GetServiceSyncPause(
	ctx context.Context, params generated.GetServiceSyncPauseParams,
) (*generated.ServiceSyncPauseResponse, error) {
	state, err := h.syncPauses.GetService(ctx, params.Stack, params.Service)
	if err != nil {
		return nil, pauseHTTPError(err)
	}
	result := toGeneratedServiceSyncPause(state)
	return &result, nil
}

// PutServiceSyncPause creates or changes the direct service pause.
func (h *handler) PutServiceSyncPause(
	ctx context.Context, request *generated.SyncPauseRequest, params generated.PutServiceSyncPauseParams,
) (*generated.ServiceSyncPauseResponse, error) {
	if _, err := h.syncPauses.SetService(ctx, params.Stack, params.Service, requestExpiration(request)); err != nil {
		return nil, pauseHTTPError(err)
	}
	state, err := h.syncPauses.GetService(ctx, params.Stack, params.Service)
	if err != nil {
		return nil, pauseHTTPError(err)
	}
	result := toGeneratedServiceSyncPause(state)
	return &result, nil
}

// DeleteServiceSyncPause removes only the direct service pause.
func (h *handler) DeleteServiceSyncPause(
	ctx context.Context, params generated.DeleteServiceSyncPauseParams,
) error {
	_, err := h.syncPauses.ResumeService(ctx, params.Stack, params.Service)
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
		ServicePauses:             make([]generated.SyncPauseRecord, 0, len(state.ServicePauses)),
		EffectiveServices:         make([]generated.EffectivePausedService, 0, len(state.EffectivelyPausedServices)),
		EffectivelyPausedServices: len(state.EffectivelyPausedServices),
	}
	if state.StackPause != nil {
		result.StackPause = generated.NewOptSyncPauseRecord(toGeneratedSyncPause(*state.StackPause))
	}
	for _, pause := range state.ServicePauses {
		result.ServicePauses = append(result.ServicePauses, toGeneratedSyncPause(pause))
	}
	directServices := make(map[string]struct{}, len(state.ServicePauses))
	for _, pause := range state.ServicePauses {
		directServices[pause.ServiceName] = struct{}{}
	}
	for _, service := range state.EffectivelyPausedServices {
		source := syncpause.EffectiveSourceService
		_, direct := directServices[service]
		if state.StackPause != nil && direct {
			source = syncpause.EffectiveSourceBoth
		} else if state.StackPause != nil {
			source = syncpause.EffectiveSourceStack
		}
		result.EffectiveServices = append(result.EffectiveServices, generated.EffectivePausedService{
			Service: service, Source: generated.EffectiveSyncPauseSource(source),
		})
	}
	return result
}

func toGeneratedServiceSyncPause(state syncpause.ServiceState) generated.ServiceSyncPauseResponse {
	result := generated.ServiceSyncPauseResponse{EffectivePaused: state.Effective}
	if state.StackPause != nil {
		result.StackPause = generated.NewOptSyncPauseRecord(toGeneratedSyncPause(*state.StackPause))
	}
	if state.ServicePause != nil {
		result.ServicePause = generated.NewOptSyncPauseRecord(toGeneratedSyncPause(*state.ServicePause))
	}
	if state.Effective {
		result.EffectiveSource = generated.NewOptEffectiveSyncPauseSource(
			generated.EffectiveSyncPauseSource(state.Source),
		)
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
	result := generated.SyncPauseRecord{
		Scope: generated.SyncPauseRecordScope(pause.Scope), Stack: pause.StackName, ExpiresAt: expires,
	}
	if pause.ServiceName != "" {
		result.Service = generated.NewOptString(pause.ServiceName)
	}
	return result
}

func pauseHTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syncpause.ErrStackNotManaged), errors.Is(err, syncpause.ErrServiceNotManaged):
		return withStatusError(http.StatusNotFound, err)
	case errors.Is(err, syncpause.ErrExpirationNotFuture):
		return withStatusError(http.StatusBadRequest, err)
	default:
		return err
	}
}
