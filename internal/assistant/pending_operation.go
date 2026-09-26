package assistant

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Stack is an optional candidate stack name.
	Stack string `json:"stack,omitempty"`
	// Service is an optional candidate service name.
	Service string `json:"service,omitempty"`
	// Replicas is the requested replica count, when present.
	Replicas *uint64 `json:"replicas,omitempty"`
}

// RouteDecision is the structured decision returned by the router model.
type RouteDecision struct {
	// Route is the selected capability route.
	Route Route `json:"route"`
	// Operation is a supported mutating operation, when recognized.
	Operation *OperationIntent `json:"operation"`
}

// PendingOperation is conversation-scoped transient state for a mutating action.
type PendingOperation struct {
	// Type is the requested operation.
	Type OperationType
	// Stage is the current deterministic workflow stage.
	Stage PendingOperationStage
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
	if current.Type != expected.Type || current.Stage != expected.Stage || current.Stack != expected.Stack ||
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

func (g *graph) startPendingOperation(conversationID string, intent OperationIntent) string {
	op := PendingOperation{Type: intent.Type, Stage: PendingOperationCollecting, Replicas: intent.Replicas}
	services := g.store.List()
	op.Stack = canonicalStackCandidate(services, intent.Stack)
	op.Service = canonicalServiceCandidate(services, intent.Service)
	resolution := resolveService(services, intent.Stack, intent.Service)
	if resolution.ok {
		op.Stack, op.Service = resolution.stack, resolution.service
	}
	if !resolution.ok {
		g.pending.set(conversationID, op)
		return resolution.message
	}
	if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
		g.pending.set(conversationID, op)
		return fmt.Sprintf("Сколько реплик установить для `%s` в стеке `%s`?", op.Service, op.Stack)
	}
	op.Stage = PendingOperationConfirmation
	g.pending.set(conversationID, op)
	return confirmationMessage(op)
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
		switch parseConfirmation(message) {
		case confirmationAffirmative:
			answer, err := g.executePendingOperation(ctx, conversationID, op)
			return true, answer, conversation.TokenUsage{}, err
		case confirmationNegative:
			g.pending.delete(conversationID)
			return true, "Операция отменена.", conversation.TokenUsage{}, nil
		default:
			if isUnrelatedPendingMessage(message) {
				g.pending.delete(conversationID)
				return false, "", conversation.TokenUsage{}, nil
			}
			return true, "Ответьте явно: `да` для подтверждения или `нет` для отмены.", conversation.TokenUsage{}, nil
		}
	case PendingOperationCollecting:
		if isUnrelatedPendingMessage(message) {
			g.pending.delete(conversationID)
			return false, "", conversation.TokenUsage{}, nil
		}
		services := g.store.List()
		stack, serviceName := candidatesFromMessage(services, message)
		if op.Stack != "" {
			stack = op.Stack
		}
		if op.Service != "" {
			serviceName = op.Service
		}
		resolution := resolveService(services, stack, serviceName)
		if !resolution.ok {
			g.pending.set(conversationID, op)
			return true, resolution.message, conversation.TokenUsage{}, nil
		}
		op.Stack, op.Service = resolution.stack, resolution.service
		if op.Type == OperationServiceReplicasSet && op.Replicas == nil {
			replicas, replicasOK := parseReplicas(message)
			if !replicasOK {
				g.pending.set(conversationID, op)
				return true, fmt.Sprintf("Сколько реплик установить для `%s` в стеке `%s`?", op.Service, op.Stack), conversation.TokenUsage{}, nil
			}
			op.Replicas = &replicas
		}
		op.Stage = PendingOperationConfirmation
		g.pending.set(conversationID, op)
		return true, confirmationMessage(op), conversation.TokenUsage{}, nil
	default:
		g.pending.delete(conversationID)
		return false, "", conversation.TokenUsage{}, nil
	}
}

func (g *graph) executePendingOperation(ctx context.Context, conversationID string, op PendingOperation) (string, error) {
	if !g.pending.claim(conversationID, op) {
		return "Операция уже обрабатывается или больше не ожидает подтверждения.", nil
	}
	resolution := resolveService(g.store.List(), op.Stack, op.Service)
	if !resolution.ok || !strings.EqualFold(resolution.stack, op.Stack) || !strings.EqualFold(resolution.service, op.Service) {
		return "Сервис изменился или больше не существует. Операция отменена.", nil
	}

	profile, _ := capabilityProfile(RouteServices)
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
	stack   string
	service string
	message string
	ok      bool
}

func resolveService(services []service.Info, stackCandidate, serviceCandidate string) serviceResolution {
	stackCandidate = strings.TrimSpace(stackCandidate)
	serviceCandidate = strings.TrimSpace(serviceCandidate)
	if stackCandidate == "" && strings.Contains(serviceCandidate, "/") {
		parts := strings.SplitN(serviceCandidate, "/", 2)
		stackCandidate, serviceCandidate = parts[0], parts[1]
	}
	if stackCandidate == "" && serviceCandidate == "" {
		return serviceResolution{message: "Уточните стек или имя сервиса."}
	}

	matches := make([]service.Info, 0)
	for _, item := range services {
		if stackCandidate != "" && !strings.EqualFold(item.Stack, stackCandidate) {
			continue
		}
		if serviceCandidate != "" && !strings.EqualFold(item.Name, serviceCandidate) {
			continue
		}
		matches = append(matches, item)
	}
	if len(matches) == 1 {
		return serviceResolution{stack: matches[0].Stack, service: matches[0].Name, ok: true}
	}
	if len(matches) > 1 {
		if serviceCandidate != "" && stackCandidate == "" {
			stacks := make([]string, 0, len(matches))
			for _, item := range matches {
				stacks = append(stacks, item.Stack)
			}
			slices.Sort(stacks)
			return serviceResolution{message: fmt.Sprintf("Сервис `%s` найден в нескольких стеках: %s. Уточните стек.", serviceCandidate, strings.Join(stacks, ", "))}
		}
		return serviceResolution{message: fmt.Sprintf("В стеке `%s` несколько сервисов. Уточните сервис.", stackCandidate)}
	}
	if stackCandidate != "" || serviceCandidate != "" {
		return serviceResolution{message: "Сервис не найден. Уточните стек и имя сервиса."}
	}
	return serviceResolution{message: "Уточните стек или имя сервиса."}
}

func canonicalStackCandidate(services []service.Info, candidate string) string {
	for _, item := range services {
		if strings.EqualFold(item.Stack, strings.TrimSpace(candidate)) {
			return item.Stack
		}
	}
	return ""
}

func canonicalServiceCandidate(services []service.Info, candidate string) string {
	if strings.Contains(candidate, "/") {
		parts := strings.SplitN(candidate, "/", 2)
		candidate = parts[1]
	}
	for _, item := range services {
		if strings.EqualFold(item.Name, strings.TrimSpace(candidate)) {
			return item.Name
		}
	}
	return ""
}

func candidatesFromMessage(services []service.Info, message string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(message))
	var stack, serviceName string
	for _, item := range services {
		if equalsOrContainsIdentifier(normalized, strings.ToLower(item.Stack)) {
			if stack != "" && !strings.EqualFold(stack, item.Stack) {
				stack = ""
			} else {
				stack = item.Stack
			}
		}
		if equalsOrContainsIdentifier(normalized, strings.ToLower(item.Name)) {
			if serviceName != "" && !strings.EqualFold(serviceName, item.Name) {
				serviceName = ""
			} else {
				serviceName = item.Name
			}
		}
	}
	return stack, serviceName
}

func equalsOrContainsIdentifier(message, identifier string) bool {
	if message == identifier {
		return true
	}
	return strings.Contains(" "+message+" ", " "+identifier+" ") || strings.Contains(message, "/"+identifier)
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
	for _, prefix := range []string{"покажи ", "расскажи ", "почему ", "как ", "что ", "show ", "list ", "why ", "how ", "what "} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	for _, action := range []string{"перезапусти", "перезапустить", "restart", "реплик", "scale"} {
		if strings.Contains(normalized, action) {
			return true
		}
	}
	return strings.ContainsFunc(normalized, func(r rune) bool { return unicode.IsPunct(r) }) && len([]rune(normalized)) > 12
}
