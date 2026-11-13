package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
)

type syncPauseService interface {
	GetStack(ctx context.Context, stack string) (syncpause.StackState, error)
	SetStack(ctx context.Context, stack string, expiresAt *time.Time) (bool, error)
	ResumeStack(ctx context.Context, stack string) (bool, error)
}

type syncPauseRequest struct {
	Stack     string     `json:"stack"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type syncPauseRecord struct {
	Stack     string     `json:"stack"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type syncPauseResult struct {
	Stack          string           `json:"stack"`
	Changed        *bool            `json:"changed,omitempty"`
	StackPause     *syncPauseRecord `json:"stack_pause,omitempty"`
	EffectivePause bool             `json:"effective_paused"`
	PausedServices []string         `json:"effectively_paused_services,omitempty"`
}

func pauseRequestSchema(includeExpiration bool) map[string]any {
	properties := map[string]any{
		"stack": map[string]any{"type": "string", "description": "GitOps stack name."},
	}
	if includeExpiration {
		properties["expires_at"] = map[string]any{
			"type": []string{"string", "null"}, "format": "date-time",
			"description": "Absolute expiration, or null for no expiration.",
		}
	}
	return map[string]any{"type": "object", "required": []string{"stack"}, "properties": properties}
}

func validatePauseTarget(request syncPauseRequest) (string, error) {
	stack := strings.TrimSpace(request.Stack)
	if stack == "" {
		return "", errors.New("stack is required")
	}
	return stack, nil
}

// SyncPauseGet reads stack-level GitOps pause policy.
type SyncPauseGet struct{ pauses syncPauseService }

func NewSyncPauseGet(pauses syncPauseService) *SyncPauseGet { return &SyncPauseGet{pauses: pauses} }

func (t *SyncPauseGet) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseGet,
		Description:          "Reads stack-level Pause Sync state from GitOps.",
		ParametersJSONSchema: pauseRequestSchema(false), Request: syncPauseRequest{}}
}

func (t *SyncPauseGet) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	stack, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	state, err := t.pauses.GetStack(ctx, stack)
	if err != nil {
		return routing.Response{}, fmt.Errorf("read stack pause: %w", err)
	}
	return routing.Response{Payload: syncPauseResult{
		Stack: stack, StackPause: pauseRecord(state.StackPause), EffectivePause: state.StackPause != nil,
		PausedServices: state.EffectivelyPausedServices,
	}}, nil
}

// SyncPauseSet creates or changes a stack-level GitOps pause.
type SyncPauseSet struct{ pauses syncPauseService }

func NewSyncPauseSet(pauses syncPauseService) *SyncPauseSet { return &SyncPauseSet{pauses: pauses} }

func (t *SyncPauseSet) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseSet,
		Description:          "Creates or changes a stack-level Pause Sync policy after explicit confirmation.",
		ParametersJSONSchema: pauseRequestSchema(true), Request: syncPauseRequest{}}
}

func (t *SyncPauseSet) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	stack, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	changed, err := t.pauses.SetStack(ctx, stack, parsed.ExpiresAt)
	if err != nil {
		return routing.Response{}, fmt.Errorf("set stack pause: %w", err)
	}
	return routing.Response{Payload: syncPauseResult{
		Stack: stack, Changed: &changed, EffectivePause: true,
	}}, nil
}

// SyncPauseResume removes a stack-level GitOps pause.
type SyncPauseResume struct{ pauses syncPauseService }

func NewSyncPauseResume(pauses syncPauseService) *SyncPauseResume {
	return &SyncPauseResume{pauses: pauses}
}

func (t *SyncPauseResume) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseResume,
		Description:          "Removes a stack-level Pause Sync policy after explicit confirmation.",
		ParametersJSONSchema: pauseRequestSchema(false), Request: syncPauseRequest{}}
}

func (t *SyncPauseResume) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	stack, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	changed, err := t.pauses.ResumeStack(ctx, stack)
	if err != nil {
		return routing.Response{}, fmt.Errorf("resume stack pause: %w", err)
	}
	return routing.Response{Payload: syncPauseResult{Stack: stack, Changed: &changed}}, nil
}

func pauseRecord(pause *syncpause.Pause) *syncPauseRecord {
	if pause == nil {
		return nil
	}
	return &syncPauseRecord{Stack: pause.StackName, ExpiresAt: pause.ExpiresAt}
}
