package assistant

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/guard"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/llm"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
)

const defaultPendingOperationTTL = 15 * time.Minute

const targetResolverMaxTokens = 96

// SyncPauseTargetResolver reads GitOps-owned Pause Sync state to validate pending operation targets.
type SyncPauseTargetResolver interface {
	// GetStack returns Pause Sync state for a GitOps stack.
	GetStack(ctx context.Context, stack string) (syncpause.StackState, error)
	// GetService returns Pause Sync state for a GitOps service.
	GetService(ctx context.Context, stack, service string) (syncpause.ServiceState, error)
}

// operationFlow runs the deterministic collect, confirm, and execute workflow for supported mutations.
type operationFlow struct {
	texts          *texts.Localizer
	modelName      string
	chat           modelCompleter
	store          ServiceStore
	tools          ToolExecutor
	guard          *guard.InjectionChecker
	allowedToolSet map[string]struct{}
	pauseTargets   SyncPauseTargetResolver
	pending        *pendingOperationStore
}

func newOperationFlow(
	config Config,
	chat modelCompleter,
	store ServiceStore,
	tools ToolExecutor,
	injectionGuard *guard.InjectionChecker,
	allowedToolSet map[string]struct{},
) *operationFlow {
	return &operationFlow{
		texts:          config.Localizer,
		modelName:      config.ModelName,
		chat:           chat,
		store:          store,
		tools:          tools,
		guard:          injectionGuard,
		allowedToolSet: allowedToolSet,
		pauseTargets:   config.SyncPauses,
		pending:        newPendingOperationStore(config.ConversationInMemoryTTL),
	}
}

func (f *operationFlow) toolSet(capabilities []Capability) map[string]struct{} {
	return capabilityToolSet(f.allowedToolSet, capabilities)
}

// OperationType identifies a mutating operation handled by the deterministic backend flow.
type OperationType string

const (
	// OperationServiceRestart restarts a service.
	OperationServiceRestart OperationType = serviceRestartTriggerTool
	// OperationServiceReplicasSet changes a service replica count.
	OperationServiceReplicasSet OperationType = serviceReplicasSetTool
	// OperationDeploySync triggers a GitOps synchronization.
	OperationDeploySync OperationType = deploySyncTriggerTool
	// OperationSyncPauseSet creates or changes a direct GitOps pause.
	OperationSyncPauseSet OperationType = syncPauseSetTool
	// OperationSyncPauseResume removes one direct GitOps pause.
	OperationSyncPauseResume OperationType = syncPauseResumeTool
)

// PendingOperationStage identifies the next input required for an operation.
type PendingOperationStage string

const (
	// PendingOperationCollecting waits for missing target or replica data.
	PendingOperationCollecting PendingOperationStage = "collecting"
	// PendingOperationConfirmation waits for explicit user confirmation.
	PendingOperationConfirmation PendingOperationStage = "confirmation"
)

// OperationIntent is a supported operation candidate extracted by the router.
type OperationIntent struct {
	// Type is the requested operation.
	Type OperationType `json:"type"`
	// Target is the literal operation target from the user message.
	Target string `json:"target,omitempty"`
	// Replicas is the requested replica count, when present.
	Replicas *uint64 `json:"replicas,omitempty"`
	// Scope is stack or service for Pause Sync operations.
	Scope string `json:"scope,omitempty"`
	// ExpiresAt is the absolute pause expiration; nil means no expiration.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// PendingOperation is conversation-scoped transient state for a mutating action.
type PendingOperation struct {
	// Type is the requested operation.
	Type OperationType
	// Stage is the current deterministic workflow stage.
	Stage PendingOperationStage
	// Target is the original unresolved target extracted by the router.
	Target string
	// Stack is the validated stack name, when resolved.
	Stack string
	// Service is the validated service name, when resolved.
	Service string
	// Replicas is the desired count for a replica change.
	Replicas *uint64
	// Scope is stack or service for Pause Sync operations.
	Scope string
	// PauseExpiresAt is nil for a pause without expiration.
	PauseExpiresAt *time.Time
	// RemainingServicePauses supports the stack-resume confirmation warning.
	RemainingServicePauses int
	// ExpiresAt is the time after which this operation is discarded.
	ExpiresAt time.Time
}

type pendingOperationStore struct {
	mu         sync.Mutex
	operations map[string]PendingOperation
	ttl        time.Duration
	now        func() time.Time
}

func newPendingOperationStore(ttl time.Duration) *pendingOperationStore {
	if ttl <= 0 {
		ttl = defaultPendingOperationTTL
	}
	return &pendingOperationStore{operations: map[string]PendingOperation{}, ttl: ttl, now: time.Now}
}

func (s *pendingOperationStore) get(conversationID string) (PendingOperation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[conversationID]
	if !ok {
		return PendingOperation{}, false
	}
	if !op.ExpiresAt.After(s.now()) {
		delete(s.operations, conversationID)
		return PendingOperation{}, false
	}
	return op, true
}

func (s *pendingOperationStore) set(conversationID string, op PendingOperation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op.ExpiresAt = s.now().Add(s.ttl)
	s.operations[conversationID] = op
}

func (s *pendingOperationStore) delete(conversationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.operations, conversationID)
}

func (s *pendingOperationStore) claim(conversationID string, expected PendingOperation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.operations[conversationID]
	if !ok || !current.ExpiresAt.After(s.now()) {
		delete(s.operations, conversationID)
		return false
	}
	if current.Type != expected.Type || current.Stage != expected.Stage ||
		current.Target != expected.Target || current.Stack != expected.Stack ||
		current.Service != expected.Service || !current.ExpiresAt.Equal(expected.ExpiresAt) ||
		!equalOptionalUint64(current.Replicas, expected.Replicas) {
		return false
	}
	if current.Scope != expected.Scope || !equalOptionalTime(current.PauseExpiresAt, expected.PauseExpiresAt) {
		return false
	}
	delete(s.operations, conversationID)
	return true
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func equalOptionalUint64(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// An operationTurn stages the next prompt's operation until its reply is saved.
// Claims and cancellations still consume the previously published operation immediately.
type operationTurn struct {
	*operationFlow
	conversationID string
	next           *PendingOperation
}

func (f *operationFlow) beginTurn(conversationID string) *operationTurn {
	return &operationTurn{operationFlow: f, conversationID: conversationID}
}

func (t *operationTurn) publish() {
	if t.next != nil {
		t.pending.set(t.conversationID, *t.next)
	}
}

func (t *operationTurn) startPendingOperation(
	ctx context.Context,
	intent OperationIntent,
) (string, conversation.TokenUsage) {
	op := PendingOperation{
		Type:           intent.Type,
		Stage:          PendingOperationCollecting,
		Target:         strings.TrimSpace(intent.Target),
		Replicas:       intent.Replicas,
		Scope:          strings.TrimSpace(intent.Scope),
		PauseExpiresAt: intent.ExpiresAt,
	}
	if op.Type == OperationDeploySync {
		op.Stage = PendingOperationConfirmation
		t.next = &op
		return t.confirmationMessage(op), conversation.TokenUsage{}
	}
	if op.Type == OperationSyncPauseSet || op.Type == OperationSyncPauseResume {
		return t.startPauseOperation(ctx, op)
	}
	services, err := t.store.ReadAll(ctx)
	if err != nil {
		return t.texts.Text("operation.catalog_failed"), conversation.TokenUsage{}
	}
	resolution, usage := t.resolveOperationTarget(ctx, services, op.Target)
	if resolution.ok {
		op.Stack, op.Service = resolution.stack, resolution.service
	}
	if !resolution.ok {
		t.next = &op
		return resolution.message, usage
	}
	if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
		t.next = &op
		return t.texts.Format("operation.ask_replicas", operationTargetData(op)), usage
	}
	op.Stage = PendingOperationConfirmation
	t.next = &op
	return t.confirmationMessage(op), usage
}

//nolint:nestif // Scope-specific target revalidation keeps stack and service warnings explicit.
func (t *operationTurn) startPauseOperation(
	ctx context.Context,
	op PendingOperation,
) (string, conversation.TokenUsage) {
	if t.pauseTargets == nil {
		return t.texts.Text("operation.pause_unavailable"), conversation.TokenUsage{}
	}
	target := strings.Trim(strings.TrimSpace(op.Target), "/")
	if op.Scope == "" {
		if strings.Contains(target, "/") {
			op.Scope = string(syncpause.ScopeService)
		} else {
			op.Scope = string(syncpause.ScopeStack)
		}
	}
	if op.Scope != string(syncpause.ScopeStack) && op.Scope != string(syncpause.ScopeService) {
		return t.texts.Text("operation.ask_scope"), conversation.TokenUsage{}
	}
	if op.Scope == string(syncpause.ScopeStack) {
		op.Stack = target
		state, err := t.pauseTargets.GetStack(ctx, op.Stack)
		if err != nil {
			return t.texts.Text("operation.stack_not_found"), conversation.TokenUsage{}
		}
		if op.Type == OperationSyncPauseResume {
			op.RemainingServicePauses = len(state.ServicePauses)
		}
	} else {
		stack, service, ok := strings.Cut(target, "/")
		if !ok || strings.TrimSpace(stack) == "" || strings.TrimSpace(service) == "" {
			return t.texts.Text("operation.ask_gitops_service"), conversation.TokenUsage{}
		}
		op.Stack, op.Service = strings.TrimSpace(stack), strings.TrimSpace(service)
		if _, err := t.pauseTargets.GetService(ctx, op.Stack, op.Service); err != nil {
			return t.texts.Text("operation.gitops_service_not_found"), conversation.TokenUsage{}
		}
	}
	op.Stage = PendingOperationConfirmation
	t.next = &op
	return t.confirmationMessage(op), conversation.TokenUsage{}
}

func (t *operationTurn) handlePendingOperation(
	ctx context.Context,
	message string,
) (bool, string, conversation.TokenUsage, error) {
	op, ok := t.pending.get(t.conversationID)
	if !ok {
		return false, "", conversation.TokenUsage{}, nil
	}

	switch op.Stage {
	case PendingOperationConfirmation:
		return t.handlePendingConfirmation(ctx, message, op)
	case PendingOperationCollecting:
		return t.handlePendingCollection(ctx, message, op)
	default:
		t.pending.delete(t.conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}
}

func (t *operationTurn) handlePendingConfirmation(
	ctx context.Context,
	message string,
	op PendingOperation,
) (bool, string, conversation.TokenUsage, error) {
	switch parseConfirmation(message) {
	case confirmationAffirmative:
		answer, err := t.executePendingOperation(ctx, op)
		return true, answer, conversation.TokenUsage{}, err
	case confirmationNegative:
		t.pending.delete(t.conversationID)
		return true, t.texts.Text("operation.cancelled"), conversation.TokenUsage{}, nil
	case confirmationUnknown:
		if isUnrelatedPendingMessage(message) {
			t.pending.delete(t.conversationID)
			return false, "", conversation.TokenUsage{}, nil
		}
		return true, t.texts.Text("operation.ask_confirmation"), conversation.TokenUsage{}, nil
	default:
		t.pending.delete(t.conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}
}

func (t *operationTurn) handlePendingCollection(
	ctx context.Context,
	message string,
	op PendingOperation,
) (bool, string, conversation.TokenUsage, error) {
	if isUnrelatedPendingMessage(message) {
		t.pending.delete(t.conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}

	var usage conversation.TokenUsage
	if op.Stack == "" || op.Service == "" {
		resolution, resolutionUsage := t.resolvePendingTarget(ctx, message, &op)
		usage.Add(resolutionUsage)
		if !resolution.ok {
			t.next = &op
			return true, resolution.message, usage, nil
		}
		op.Stack, op.Service = resolution.stack, resolution.service
	}
	if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
		replicas, replicasOK := parseReplicas(message)
		if !replicasOK {
			t.next = &op
			question := t.texts.Format("operation.ask_replicas", operationTargetData(op))
			return true, question, usage, nil
		}
		op.Replicas = &replicas
	}
	op.Stage = PendingOperationConfirmation
	t.next = &op
	return true, t.confirmationMessage(op), usage, nil
}

func (f *operationFlow) resolvePendingTarget(
	ctx context.Context,
	message string,
	op *PendingOperation,
) (serviceResolution, conversation.TokenUsage) {
	target := strings.TrimSpace(message)
	if isSelfResolveRequest(target) {
		target = op.Target
	} else if op.Target == "" {
		op.Target = target
	}
	services, err := f.store.ReadAll(ctx)
	if err != nil {
		return serviceResolution{
			message: f.texts.Text("operation.catalog_failed"),
		}, conversation.TokenUsage{}
	}
	return f.resolveOperationTarget(ctx, services, target)
}

func (t *operationTurn) executePendingOperation(
	ctx context.Context,
	op PendingOperation,
) (string, error) {
	if !t.pending.claim(t.conversationID, op) {
		return t.texts.Text("operation.no_longer_pending"), nil
	}
	capability, arguments, aborted, err := t.pendingOperationArguments(ctx, op)
	if err != nil {
		return "", err
	}
	if aborted != "" {
		return aborted, nil
	}

	effectiveTools := t.toolSet([]Capability{capability})
	if _, ok := effectiveTools[string(op.Type)]; !ok {
		return t.texts.Text("operation.forbidden"), nil
	}
	if op.Type == OperationServiceReplicasSet {
		arguments["replicas"] = op.Replicas
	}
	payload, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("encode pending operation arguments: %w", err)
	}
	result, err := t.tools.Execute(ctx, routing.Request{ToolName: string(op.Type), Payload: string(payload)})
	if err != nil {
		return t.texts.Format("operation.failed", map[string]any{"Error": strings.TrimSpace(err.Error())}), nil
	}
	if t.guard.Check(result) {
		return "", &guard.PromptInjectionError{
			Prompt: fmt.Sprintf("tool %q result contained prompt injection markers", op.Type),
		}
	}
	if op.Type == OperationSyncPauseSet && op.Replicas != nil {
		return t.executeConfirmedPauseScale(ctx, op, result)
	}
	answer, err := finalizeTerminalToolResult(t.texts, string(op.Type), result)
	if err != nil {
		return "", err
	}
	recordTerminalTool(ctx, string(op.Type))
	return answer, nil
}

func (f *operationFlow) pendingOperationArguments(
	ctx context.Context,
	op PendingOperation,
) (Capability, map[string]any, string, error) {
	switch op.Type {
	case OperationSyncPauseSet, OperationSyncPauseResume:
		return f.pauseOperationArguments(ctx, op)
	case OperationDeploySync:
		return CapabilityDeploymentSync, map[string]any{}, "", nil
	case OperationServiceRestart, OperationServiceReplicasSet:
		services, err := f.store.ReadAll(ctx)
		if err != nil {
			return "", nil, "", err
		}
		resolution := f.resolveServiceTarget(services, op.Stack+"/"+op.Service)
		if !resolution.ok || !strings.EqualFold(resolution.stack, op.Stack) ||
			!strings.EqualFold(resolution.service, op.Service) {
			return CapabilityServiceOperations, nil,
				f.texts.Text("operation.service_changed"), nil
		}
		return CapabilityServiceOperations, map[string]any{
			"stack": resolution.stack, "service": resolution.service,
		}, "", nil
	default:
		return "", nil, "", fmt.Errorf("unsupported pending operation %q", op.Type)
	}
}

func (f *operationFlow) pauseOperationArguments(
	ctx context.Context,
	op PendingOperation,
) (Capability, map[string]any, string, error) {
	if f.pauseTargets == nil {
		return CapabilityPauseSync, nil, f.texts.Text("operation.pause_unavailable"), nil
	}
	arguments := map[string]any{"scope": op.Scope, "stack": op.Stack}
	if op.Scope == string(syncpause.ScopeService) {
		arguments["service"] = op.Service
	}
	if op.Type == OperationSyncPauseSet {
		arguments["expires_at"] = op.PauseExpiresAt
	}
	if op.Scope == string(syncpause.ScopeStack) {
		if !f.stackPauseTargetValid(ctx, op.Stack) {
			return CapabilityPauseSync, nil,
				f.texts.Text("operation.gitops_stack_changed"), nil
		}
		return CapabilityPauseSync, arguments, "", nil
	}
	if !f.servicePauseTargetValid(ctx, op.Stack, op.Service) {
		return CapabilityPauseSync, nil,
			f.texts.Text("operation.gitops_service_changed"), nil
	}
	return CapabilityPauseSync, arguments, "", nil
}

func (f *operationFlow) stackPauseTargetValid(ctx context.Context, stack string) bool {
	_, err := f.pauseTargets.GetStack(ctx, stack)
	return err == nil
}

func (f *operationFlow) servicePauseTargetValid(ctx context.Context, stack, service string) bool {
	_, err := f.pauseTargets.GetService(ctx, stack, service)
	return err == nil
}

func (t *operationTurn) executeConfirmedPauseScale(
	ctx context.Context,
	op PendingOperation,
	pauseResult string,
) (string, error) {
	pauseAnswer, err := finalizeTerminalToolResult(t.texts, string(op.Type), pauseResult)
	if err != nil {
		return "", err
	}
	if op.Scope != string(syncpause.ScopeService) {
		return t.texts.Format("operation.scale_scope_required", map[string]any{"PauseResult": pauseAnswer}), nil
	}
	retry := PendingOperation{Type: OperationServiceReplicasSet, Stage: PendingOperationConfirmation,
		Target: op.Stack + "/" + op.Service, Stack: op.Stack, Service: op.Service, Replicas: op.Replicas}
	serviceTools := t.toolSet([]Capability{CapabilityServiceOperations})
	if _, ok := serviceTools[serviceReplicasSetTool]; !ok {
		t.next = &retry
		return t.texts.Format("operation.scale_forbidden", map[string]any{"PauseResult": pauseAnswer}), nil
	}
	scalePayload, err := json.Marshal(map[string]any{
		"stack": op.Stack, "service": op.Service, "replicas": op.Replicas,
	})
	if err != nil {
		return "", err
	}
	scaleResult, scaleErr := t.tools.Execute(ctx, routing.Request{
		ToolName: serviceReplicasSetTool, Payload: string(scalePayload),
	})
	if scaleErr != nil {
		t.next = &retry
		return t.texts.Format("operation.scale_failed", map[string]any{
			"PauseResult": pauseAnswer, "Error": strings.TrimSpace(scaleErr.Error()),
		}), nil
	}
	if t.guard.Check(scaleResult) {
		return "", &guard.PromptInjectionError{Prompt: "replica tool result contained prompt injection markers"}
	}
	scaleAnswer, err := finalizeTerminalToolResult(t.texts, serviceReplicasSetTool, scaleResult)
	if err != nil {
		return "", err
	}
	recordTerminalTool(ctx, string(op.Type))
	recordTerminalTool(ctx, serviceReplicasSetTool)
	return pauseAnswer + " " + scaleAnswer, nil
}

type serviceResolution struct {
	stack     string
	service   string
	message   string
	ambiguous bool
	ok        bool
}

func (f *operationFlow) resolveServiceTarget(services []model.Info, target string) serviceResolution {
	target = strings.TrimSpace(target)
	if target == "" {
		return serviceResolution{message: f.texts.Text("operation.ask_target")}
	}

	for _, item := range services {
		if strings.EqualFold(serviceTarget(item), target) {
			return resolvedService(item)
		}
	}

	matches := matchingServices(services, func(item model.Info) bool {
		return strings.EqualFold(item.Name, target)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return f.ambiguousServiceResolution(matches)
	}

	matches = matchingServices(services, func(item model.Info) bool {
		return strings.EqualFold(item.Stack, target)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return f.ambiguousServiceResolution(matches)
	}

	normalizedTarget := strings.ToLower(target)
	matches = matchingServices(services, func(item model.Info) bool {
		return strings.Contains(strings.ToLower(serviceTarget(item)), normalizedTarget) ||
			strings.Contains(strings.ToLower(item.Name), normalizedTarget) ||
			strings.Contains(strings.ToLower(item.Stack), normalizedTarget)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return f.ambiguousServiceResolution(matches)
	}

	return serviceResolution{
		message: f.texts.Format("operation.target_not_found", map[string]any{"Target": target}),
	}
}

func matchingServices(services []model.Info, matches func(model.Info) bool) []model.Info {
	result := make([]model.Info, 0)
	for _, item := range services {
		if matches(item) {
			result = append(result, item)
		}
	}
	return result
}

func resolvedService(item model.Info) serviceResolution {
	return serviceResolution{stack: item.Stack, service: item.Name, ok: true}
}

func (f *operationFlow) ambiguousServiceResolution(services []model.Info) serviceResolution {
	targets := make([]string, 0, len(services))
	seen := make(map[string]struct{}, len(services))
	for _, item := range services {
		target := serviceTarget(item)
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	slices.Sort(targets)
	quoted := make([]string, 0, len(targets))
	for _, target := range targets {
		quoted = append(quoted, "`"+target+"`")
	}
	return serviceResolution{
		message:   f.texts.Format("operation.ambiguous_target", map[string]any{"Targets": strings.Join(quoted, ", ")}),
		ambiguous: true,
	}
}

func serviceTarget(item model.Info) string {
	return strings.TrimSpace(item.Stack) + "/" + strings.TrimSpace(item.Name)
}

type targetResolverDecision struct {
	Status     string   `json:"status"`
	Candidate  string   `json:"candidate,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

func (f *operationFlow) resolveOperationTarget(
	ctx context.Context,
	services []model.Info,
	target string,
) (serviceResolution, conversation.TokenUsage) {
	deterministic := f.resolveServiceTarget(services, target)
	if deterministic.ok || deterministic.ambiguous || strings.TrimSpace(target) == "" || len(services) == 0 {
		return deterministic, conversation.TokenUsage{}
	}

	candidates := make([]string, 0, len(services))
	for _, item := range services {
		candidates = append(candidates, serviceTarget(item))
	}
	slices.Sort(candidates)
	completion, err := f.chat.Complete(ctx, llm.Request{
		Model:       f.modelName,
		Temperature: 0,
		MaxTokens:   targetResolverMaxTokens,
		Messages: []llm.Message{
			{Role: "system", Content: targetResolverSystemPrompt},
			{Role: "user", Content: "Target: " + strings.TrimSpace(target) + "\nCandidates:\n" + strings.Join(candidates, "\n")},
		},
	})
	if err != nil {
		slog.WarnContext(ctx, "[assistant] service target resolver fallback failed", slog.Any("err", err))
		return deterministic, completion.Usage
	}

	resolution := f.validateTargetResolverDecision(services, completion.Content)
	if resolution.ok || resolution.message != "" {
		return resolution, completion.Usage
	}
	return deterministic, completion.Usage
}

func (f *operationFlow) validateTargetResolverDecision(services []model.Info, content string) serviceResolution {
	var decision targetResolverDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decision); err != nil {
		return serviceResolution{}
	}
	switch strings.ToLower(strings.TrimSpace(decision.Status)) {
	case "exact":
		for _, item := range services {
			if strings.EqualFold(serviceTarget(item), strings.TrimSpace(decision.Candidate)) {
				return resolvedService(item)
			}
		}
	case "ambiguous":
		matches := targetResolverMatches(services, decision.Candidates)
		if len(matches) > 1 {
			return f.ambiguousServiceResolution(matches)
		}
	case "not_found":
		return serviceResolution{}
	}
	return serviceResolution{}
}

func targetResolverMatches(services []model.Info, candidates []string) []model.Info {
	matches := make([]model.Info, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		for _, item := range services {
			canonical := serviceTarget(item)
			if !strings.EqualFold(canonical, strings.TrimSpace(candidate)) {
				continue
			}
			if _, ok := seen[canonical]; ok {
				continue
			}
			seen[canonical] = struct{}{}
			matches = append(matches, item)
		}
	}
	return matches
}

const targetResolverSystemPrompt = `Resolve one Docker Swarm service target against the provided candidates.
Return compact JSON only, using one form:
{"status":"exact","candidate":"stack/service"}
{"status":"ambiguous","candidates":["stack/service"]}
{"status":"not_found"}
Candidate values must be copied exactly from the provided list. Do not use tools or outside knowledge.`

func isSelfResolveRequest(message string) bool {
	switch strings.ToLower(strings.TrimSpace(message)) {
	case "найди сам", "найдите сами", "определи сам", "find it", "find it yourself", "resolve it":
		return true
	default:
		return false
	}
}

func (f *operationFlow) confirmationMessage(op PendingOperation) string {
	data := operationTargetData(op)
	switch op.Type {
	case OperationDeploySync:
		return f.texts.Text("operation.confirm_sync")
	case OperationServiceReplicasSet:
		return f.confirmReplicaCount("operation.confirm_replicas", *op.Replicas, data)
	case OperationSyncPauseSet:
		return f.confirmPause(op, data)
	case OperationSyncPauseResume:
		if op.Scope == string(syncpause.ScopeService) {
			return f.texts.Format("operation.confirm_resume_service", data)
		}
		question := f.texts.Format("operation.confirm_resume_stack", data)
		if count := op.RemainingServicePauses; count > 0 {
			question += " " + f.texts.Plural("operation.remaining_pauses", count, map[string]any{"Count": count})
		}
		return question
	case OperationServiceRestart:
		return f.texts.Format("operation.confirm_restart", data)
	default:
		return ""
	}
}

func (f *operationFlow) confirmPause(op PendingOperation, data map[string]any) string {
	scope := string(syncpause.ScopeStack)
	if op.Scope == string(syncpause.ScopeService) {
		scope = string(syncpause.ScopeService)
	}
	suffix := ""
	if op.PauseExpiresAt != nil {
		data["Until"] = op.PauseExpiresAt.UTC().Format(time.RFC3339)
		suffix = "_until"
	}
	if op.Replicas != nil {
		return f.confirmReplicaCount("operation.confirm_pause_scale_"+scope+suffix, *op.Replicas, data)
	}
	return f.texts.Format("operation.confirm_pause_"+scope+suffix, data)
}

func (f *operationFlow) confirmReplicaCount(id string, count uint64, data map[string]any) string {
	data["Count"] = count
	// go-i18n accepts signed integers. For larger values preserve the last two
	// digits (used by Russian plurals) and the distinction from English singular.
	pluralCount := count
	if pluralCount > math.MaxInt64 {
		const pluralSuffixBase = 100
		pluralCount = pluralSuffixBase + count%pluralSuffixBase
	}
	return f.texts.Plural(id, int64(pluralCount), data)
}

func operationTargetData(op PendingOperation) map[string]any {
	return map[string]any{"Stack": op.Stack, "Service": op.Service}
}

type confirmationValue uint8

const (
	confirmationUnknown confirmationValue = iota
	confirmationAffirmative
	confirmationNegative
)

func parseConfirmation(message string) confirmationValue {
	switch strings.ToLower(strings.TrimSpace(message)) {
	case "да", "yes", "confirm", "подтверждаю":
		return confirmationAffirmative
	case "нет", "no", "cancel", "отмена", "отменить":
		return confirmationNegative
	default:
		return confirmationUnknown
	}
}

func parseReplicas(message string) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(message), 10, 64)
	return value, err == nil && value > 0
}

func isUnrelatedPendingMessage(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	prefixes := []string{
		"покажи ", "расскажи ", "почему ", "как ", "что ",
		"show ", "list ", "why ", "how ", "what ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	for _, action := range []string{"перезапусти", "перезапустить", "restart", "реплик", "scale"} {
		if strings.Contains(normalized, action) {
			return true
		}
	}
	return strings.ContainsFunc(normalized, unicode.IsPunct) && len([]rune(normalized)) > 12
}
