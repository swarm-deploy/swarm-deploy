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
	GetService(ctx context.Context, stack, service string) (syncpause.ServiceState, error)
	SetStack(ctx context.Context, stack string, expiresAt *time.Time) (bool, error)
	SetService(ctx context.Context, stack, service string, expiresAt *time.Time) (bool, error)
	ResumeStack(ctx context.Context, stack string) (bool, error)
	ResumeService(ctx context.Context, stack, service string) (bool, error)
}

type syncPauseRequest struct {
	Scope     string     `json:"scope"`
	Stack     string     `json:"stack"`
	Service   string     `json:"service,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type syncPauseRecord struct {
	Scope     string     `json:"scope"`
	Stack     string     `json:"stack"`
	Service   string     `json:"service,omitempty"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type syncPauseResult struct {
	Scope             string                      `json:"scope"`
	Stack             string                      `json:"stack"`
	Service           string                      `json:"service,omitempty"`
	Changed           *bool                       `json:"changed,omitempty"`
	StackPause        *syncPauseRecord            `json:"stack_pause,omitempty"`
	ServicePause      *syncPauseRecord            `json:"service_pause,omitempty"`
	ServicePauses     []syncPauseRecord           `json:"service_pauses,omitempty"`
	EffectivePaused   bool                        `json:"effective_paused"`
	EffectiveSource   string                      `json:"effective_source,omitempty"`
	PausedServices    []string                    `json:"effectively_paused_services,omitempty"`
	EffectiveServices []syncPauseEffectiveService `json:"effective_services,omitempty"`
}

type syncPauseEffectiveService struct {
	Service string `json:"service"`
	Source  string `json:"source"`
}

func pauseRequestSchema(includeExpiration bool) map[string]any {
	properties := map[string]any{
		"scope":   map[string]any{"type": "string", "enum": []string{"stack", "service"}},
		"stack":   map[string]any{"type": "string", "description": "GitOps stack name."},
		"service": map[string]any{"type": "string", "description": "Required for service scope."},
	}
	if includeExpiration {
		properties["expires_at"] = map[string]any{
			"type": []string{"string", "null"}, "format": "date-time",
			"description": "Absolute expiration, or null for no expiration.",
		}
	}
	return map[string]any{"type": "object", "required": []string{"scope", "stack"}, "properties": properties}
}

func validatePauseTarget(request syncPauseRequest) (syncpause.Scope, error) {
	request.Scope = strings.TrimSpace(request.Scope)
	request.Stack = strings.TrimSpace(request.Stack)
	request.Service = strings.TrimSpace(request.Service)
	if request.Stack == "" {
		return "", errors.New("stack is required")
	}
	scope := syncpause.Scope(request.Scope)
	if scope != syncpause.ScopeStack && scope != syncpause.ScopeService {
		return "", errors.New("scope must be stack or service")
	}
	if scope == syncpause.ScopeService && request.Service == "" {
		return "", errors.New("service is required for service scope")
	}
	return scope, nil
}

// SyncPauseGet reads direct and effective GitOps pause policy.
type SyncPauseGet struct{ pauses syncPauseService }

func NewSyncPauseGet(pauses syncPauseService) *SyncPauseGet { return &SyncPauseGet{pauses: pauses} }

func (t *SyncPauseGet) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseGet,
		Description:          "Reads direct and effective Pause Sync state from GitOps.",
		ParametersJSONSchema: pauseRequestSchema(false), Request: syncPauseRequest{}}
}

func (t *SyncPauseGet) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	scope, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	if scope == syncpause.ScopeStack {
		state, getErr := t.pauses.GetStack(ctx, parsed.Stack)
		if getErr != nil {
			return routing.Response{}, fmt.Errorf("read stack pause: %w", getErr)
		}
		result := syncPauseResult{Scope: string(scope), Stack: parsed.Stack,
			EffectivePaused:   len(state.EffectivelyPausedServices) > 0,
			PausedServices:    state.EffectivelyPausedServices,
			ServicePauses:     make([]syncPauseRecord, 0, len(state.ServicePauses)),
			EffectiveServices: make([]syncPauseEffectiveService, 0, len(state.EffectivelyPausedServices))}
		result.StackPause = pauseRecord(state.StackPause)
		direct := make(map[string]struct{}, len(state.ServicePauses))
		for i := range state.ServicePauses {
			result.ServicePauses = append(result.ServicePauses, *pauseRecord(&state.ServicePauses[i]))
			direct[state.ServicePauses[i].ServiceName] = struct{}{}
		}
		for _, service := range state.EffectivelyPausedServices {
			source := syncpause.EffectiveSourceService
			_, serviceDirect := direct[service]
			if state.StackPause != nil && serviceDirect {
				source = syncpause.EffectiveSourceBoth
			} else if state.StackPause != nil {
				source = syncpause.EffectiveSourceStack
			}
			result.EffectiveServices = append(result.EffectiveServices, syncPauseEffectiveService{
				Service: service, Source: string(source),
			})
		}
		return routing.Response{Payload: result}, nil
	}
	state, getErr := t.pauses.GetService(ctx, parsed.Stack, parsed.Service)
	if getErr != nil {
		return routing.Response{}, fmt.Errorf("read service pause: %w", getErr)
	}
	return routing.Response{Payload: syncPauseResult{Scope: string(scope), Stack: parsed.Stack, Service: parsed.Service,
		StackPause: pauseRecord(state.StackPause), ServicePause: pauseRecord(state.ServicePause),
		EffectivePaused: state.Effective, EffectiveSource: string(state.Source)}}, nil
}

// SyncPauseSet creates or changes a direct GitOps pause.
type SyncPauseSet struct{ pauses syncPauseService }

func NewSyncPauseSet(pauses syncPauseService) *SyncPauseSet { return &SyncPauseSet{pauses: pauses} }

func (t *SyncPauseSet) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseSet,
		Description:          "Creates or changes a direct Pause Sync policy after explicit confirmation.",
		ParametersJSONSchema: pauseRequestSchema(true), Request: syncPauseRequest{}}
}

func (t *SyncPauseSet) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	scope, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	var changed bool
	if scope == syncpause.ScopeStack {
		changed, err = t.pauses.SetStack(ctx, parsed.Stack, parsed.ExpiresAt)
	} else {
		changed, err = t.pauses.SetService(ctx, parsed.Stack, parsed.Service, parsed.ExpiresAt)
	}
	if err != nil {
		return routing.Response{}, fmt.Errorf("set pause: %w", err)
	}
	return routing.Response{Payload: syncPauseResult{Scope: string(scope), Stack: parsed.Stack,
		Service: parsed.Service, Changed: &changed, EffectivePaused: true, EffectiveSource: string(scope)}}, nil
}

// SyncPauseResume removes only the requested direct pause.
type SyncPauseResume struct{ pauses syncPauseService }

func NewSyncPauseResume(pauses syncPauseService) *SyncPauseResume {
	return &SyncPauseResume{pauses: pauses}
}

func (t *SyncPauseResume) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{Name: config.AssistantToolNameSyncPauseResume,
		Description:          "Removes one direct Pause Sync policy after explicit confirmation.",
		ParametersJSONSchema: pauseRequestSchema(false), Request: syncPauseRequest{}}
}

func (t *SyncPauseResume) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsed, err := convertRequestPayload[syncPauseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	scope, err := validatePauseTarget(parsed)
	if err != nil {
		return routing.Response{}, err
	}
	var changed bool
	if scope == syncpause.ScopeStack {
		changed, err = t.pauses.ResumeStack(ctx, parsed.Stack)
	} else {
		changed, err = t.pauses.ResumeService(ctx, parsed.Stack, parsed.Service)
	}
	if err != nil {
		return routing.Response{}, fmt.Errorf("resume pause: %w", err)
	}
	return routing.Response{Payload: syncPauseResult{Scope: string(scope), Stack: parsed.Stack,
		Service: parsed.Service, Changed: &changed}}, nil
}

func pauseRecord(pause *syncpause.Pause) *syncPauseRecord {
	if pause == nil {
		return nil
	}
	return &syncPauseRecord{Scope: string(pause.Scope), Stack: pause.StackName,
		Service: pause.ServiceName, ExpiresAt: pause.ExpiresAt}
}
