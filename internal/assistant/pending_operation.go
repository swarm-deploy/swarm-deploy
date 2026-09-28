package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
)

const defaultPendingOperationTTL = 15 * time.Minute

const targetResolverMaxTokens = 96

// OperationType identifies a mutating operation handled by the deterministic backend flow.
type OperationType string

const (
	// OperationServiceRestart restarts a service.
	OperationServiceRestart OperationType = serviceRestartTriggerTool
	// OperationServiceReplicasSet changes a service replica count.
	OperationServiceReplicasSet OperationType = serviceReplicasSetTool
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
}

// RouteDecision is the structured decision returned by the router model.
type RouteDecision struct {
	// Route is the selected route.
	Route Route `json:"route"`
	// Capabilities are the composable context and tool groups required by this request.
	// Nil means the router omitted the field and route defaults should be used for compatibility.
	Capabilities []Capability `json:"capabilities"`
	// Operation is a supported mutating operation, when recognized.
	Operation *OperationIntent `json:"operation"`
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
	delete(s.operations, conversationID)
	return true
}

func equalOptionalUint64(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (t OperationType) supported() bool {
	return t == OperationServiceRestart || t == OperationServiceReplicasSet
}

func (g *graph) startPendingOperation(
	ctx context.Context,
	conversationID string,
	intent OperationIntent,
) (string, conversation.TokenUsage) {
	op := PendingOperation{
		Type:     intent.Type,
		Stage:    PendingOperationCollecting,
		Target:   strings.TrimSpace(intent.Target),
		Replicas: intent.Replicas,
	}
	services := g.store.List()
	resolution, usage := g.resolveOperationTarget(ctx, services, op.Target)
	if resolution.ok {
		op.Stack, op.Service = resolution.stack, resolution.service
	}
	if !resolution.ok {
		g.pending.set(conversationID, op)
		return resolution.message, usage
	}
	if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
		g.pending.set(conversationID, op)
		return fmt.Sprintf("Сколько реплик установить для `%s` в стеке `%s`?", op.Service, op.Stack), usage
	}
	op.Stage = PendingOperationConfirmation
	g.pending.set(conversationID, op)
	return confirmationMessage(op), usage
}

func (g *graph) handlePendingOperation(
	ctx context.Context,
	conversationID string,
	message string,
) (bool, string, conversation.TokenUsage, error) {
	op, ok := g.pending.get(conversationID)
	if !ok {
		return false, "", conversation.TokenUsage{}, nil
	}

	switch op.Stage {
	case PendingOperationConfirmation:
		return g.handlePendingConfirmation(ctx, conversationID, message, op)
	case PendingOperationCollecting:
		return g.handlePendingCollection(ctx, conversationID, message, op)
	default:
		g.pending.delete(conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}
}

func (g *graph) handlePendingConfirmation(
	ctx context.Context,
	conversationID string,
	message string,
	op PendingOperation,
) (bool, string, conversation.TokenUsage, error) {
	switch parseConfirmation(message) {
	case confirmationAffirmative:
		answer, err := g.executePendingOperation(ctx, conversationID, op)
		return true, answer, conversation.TokenUsage{}, err
	case confirmationNegative:
		g.pending.delete(conversationID)
		return true, "Операция отменена.", conversation.TokenUsage{}, nil
	case confirmationUnknown:
		if isUnrelatedPendingMessage(message) {
			g.pending.delete(conversationID)
			return false, "", conversation.TokenUsage{}, nil
		}
		return true, "Ответьте явно: `да` для подтверждения или `нет` для отмены.", conversation.TokenUsage{}, nil
	default:
		g.pending.delete(conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}
}

func (g *graph) handlePendingCollection(
	ctx context.Context,
	conversationID string,
	message string,
	op PendingOperation,
) (bool, string, conversation.TokenUsage, error) {
	if isUnrelatedPendingMessage(message) {
		g.pending.delete(conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}

	var usage conversation.TokenUsage
	if op.Stack == "" || op.Service == "" {
		resolution, resolutionUsage := g.resolvePendingTarget(ctx, message, &op)
		usage.Add(resolutionUsage)
		if !resolution.ok {
			g.pending.set(conversationID, op)
			return true, resolution.message, usage, nil
		}
		op.Stack, op.Service = resolution.stack, resolution.service
	}
	if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
		replicas, replicasOK := parseReplicas(message)
		if !replicasOK {
			g.pending.set(conversationID, op)
			question := fmt.Sprintf(
				"Сколько реплик установить для `%s` в стеке `%s`?",
				op.Service,
				op.Stack,
			)
			return true, question, usage, nil
		}
		op.Replicas = &replicas
	}
	op.Stage = PendingOperationConfirmation
	g.pending.set(conversationID, op)
	return true, confirmationMessage(op), usage, nil
}

func (g *graph) resolvePendingTarget(
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
	return g.resolveOperationTarget(ctx, g.store.List(), target)
}

func (g *graph) executePendingOperation(
	ctx context.Context,
	conversationID string,
	op PendingOperation,
) (string, error) {
	if !g.pending.claim(conversationID, op) {
		return "Операция уже обрабатывается или больше не ожидает подтверждения.", nil
	}
	resolution := resolveServiceTarget(g.store.List(), op.Stack+"/"+op.Service)
	if !resolution.ok || !strings.EqualFold(resolution.stack, op.Stack) ||
		!strings.EqualFold(resolution.service, op.Service) {
		return "Сервис изменился или больше не существует. Операция отменена.", nil
	}

	profile, _ := routeProfile(RouteServices)
	effectiveTools := g.effectiveToolSet(profile)
	if _, ok := effectiveTools[string(op.Type)]; !ok {
		return "Эта операция запрещена настройкой `assistant.tools`.", nil
	}
	arguments := map[string]any{"stack": resolution.stack, "service": resolution.service}
	if op.Type == OperationServiceReplicasSet {
		arguments["replicas"] = op.Replicas
	}
	payload, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("encode pending operation arguments: %w", err)
	}
	result, err := g.tools.Execute(ctx, routing.Request{ToolName: string(op.Type), Payload: string(payload)})
	if err != nil {
		return fmt.Sprintf("Не удалось выполнить операцию: %s", strings.TrimSpace(err.Error())), nil
	}
	if g.guard.Check(result) {
		return "", &promptInjectionError{prompt: fmt.Sprintf("tool %q result contained prompt injection markers", op.Type)}
	}
	answer, err := finalizeTerminalToolResult(string(op.Type), result)
	if err != nil {
		return "", err
	}
	recordTerminalTool(ctx, string(op.Type))
	return answer, nil
}

type serviceResolution struct {
	stack     string
	service   string
	message   string
	ambiguous bool
	ok        bool
}

func resolveServiceTarget(services []service.Info, target string) serviceResolution {
	target = strings.TrimSpace(target)
	if target == "" {
		return serviceResolution{message: "Уточните стек или имя сервиса."}
	}

	for _, item := range services {
		if strings.EqualFold(serviceTarget(item), target) {
			return resolvedService(item)
		}
	}

	matches := matchingServices(services, func(item service.Info) bool {
		return strings.EqualFold(item.Name, target)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return ambiguousServiceResolution(matches)
	}

	matches = matchingServices(services, func(item service.Info) bool {
		return strings.EqualFold(item.Stack, target)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return ambiguousServiceResolution(matches)
	}

	normalizedTarget := strings.ToLower(target)
	matches = matchingServices(services, func(item service.Info) bool {
		return strings.Contains(strings.ToLower(serviceTarget(item)), normalizedTarget) ||
			strings.Contains(strings.ToLower(item.Name), normalizedTarget) ||
			strings.Contains(strings.ToLower(item.Stack), normalizedTarget)
	})
	if len(matches) == 1 {
		return resolvedService(matches[0])
	}
	if len(matches) > 1 {
		return ambiguousServiceResolution(matches)
	}

	return serviceResolution{
		message: fmt.Sprintf("Сервис для цели `%s` не найден. Уточните стек или имя сервиса.", target),
	}
}

func matchingServices(services []service.Info, matches func(service.Info) bool) []service.Info {
	result := make([]service.Info, 0)
	for _, item := range services {
		if matches(item) {
			result = append(result, item)
		}
	}
	return result
}

func resolvedService(item service.Info) serviceResolution {
	return serviceResolution{stack: item.Stack, service: item.Name, ok: true}
}

func ambiguousServiceResolution(services []service.Info) serviceResolution {
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
		message:   "Найдено несколько сервисов: " + strings.Join(quoted, ", ") + ". Уточните цель.",
		ambiguous: true,
	}
}

func serviceTarget(item service.Info) string {
	return strings.TrimSpace(item.Stack) + "/" + strings.TrimSpace(item.Name)
}

type targetResolverDecision struct {
	Status     string   `json:"status"`
	Candidate  string   `json:"candidate,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

func (g *graph) resolveOperationTarget(
	ctx context.Context,
	services []service.Info,
	target string,
) (serviceResolution, conversation.TokenUsage) {
	deterministic := resolveServiceTarget(services, target)
	if deterministic.ok || deterministic.ambiguous || strings.TrimSpace(target) == "" || len(services) == 0 {
		return deterministic, conversation.TokenUsage{}
	}

	candidates := make([]string, 0, len(services))
	for _, item := range services {
		candidates = append(candidates, serviceTarget(item))
	}
	slices.Sort(candidates)
	completion, err := g.chat.complete(ctx, modelRequest{
		Model:       g.config.ModelName,
		Temperature: 0,
		MaxTokens:   targetResolverMaxTokens,
		Messages: []modelMessage{
			{Role: "system", Content: targetResolverSystemPrompt},
			{Role: "user", Content: "Target: " + strings.TrimSpace(target) + "\nCandidates:\n" + strings.Join(candidates, "\n")},
		},
	})
	if err != nil {
		slog.WarnContext(ctx, "[assistant] service target resolver fallback failed", slog.Any("err", err))
		return deterministic, conversation.TokenUsage{}
	}

	resolution := validateTargetResolverDecision(services, completion.Content)
	if resolution.ok || resolution.message != "" {
		return resolution, completion.Usage
	}
	return deterministic, completion.Usage
}

func validateTargetResolverDecision(services []service.Info, content string) serviceResolution {
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
			return ambiguousServiceResolution(matches)
		}
	case "not_found":
		return serviceResolution{}
	}
	return serviceResolution{}
}

func targetResolverMatches(services []service.Info, candidates []string) []service.Info {
	matches := make([]service.Info, 0, len(candidates))
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

func confirmationMessage(op PendingOperation) string {
	if op.Type == OperationServiceReplicasSet && op.Replicas != nil {
		return fmt.Sprintf("Установить %d реплик для `%s` в стеке `%s`?", *op.Replicas, op.Service, op.Stack)
	}
	return fmt.Sprintf("Перезапустить `%s` в стеке `%s`?", op.Service, op.Stack)
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
