package assistant

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/llm"
)

const (
	routerMaxTokens     = 256
	routerHistoryTurns  = 6
	routerExtraMessages = 2
)

type Route string

const (
	RouteGeneral            Route = "general"
	RouteOutOfScope         Route = "out_of_scope"
	RouteNeedsClarification Route = "needs_clarification"
	RoutePlatform           Route = "platform"
	RouteServices           Route = "services"
	RouteCluster            Route = "cluster"
	RouteDeployments        Route = "deployments"
	RouteDiagnostics        Route = "diagnostics"
	RouteLookups            Route = "lookups"
)

var (
	errInvalidRouterResponse = errors.New("invalid router response")
	errUnknownRoute          = errors.New("unknown route")
)

type RouteRequest struct {
	Message       string
	RecentHistory []conversation.Turn
}

type RouteResult struct {
	// Route is the selected route.
	Route Route
	// Capabilities are the context and tool groups needed for the supported request.
	Capabilities []Capability
	// Operation is the supported mutation to handle through the confirmation flow.
	Operation *OperationIntent
	// ScopedRequest contains the supported parts of a mixed request.
	ScopedRequest string
	// Usage is the token usage of the routing completion.
	Usage conversation.TokenUsage
}

// RouteDecision is the structured decision returned by the router model.
type RouteDecision struct {
	// Route is the selected route.
	Route Route `json:"route"`
	// Capabilities are the context and tool groups required by the supported request.
	// Nil means the router omitted the field and route defaults should be used for compatibility.
	Capabilities []Capability `json:"capabilities"`
	// Operation is a supported mutating operation, when recognized.
	Operation *OperationIntent `json:"operation"`
	// ScopedRequest contains only the supported parts of a mixed request.
	ScopedRequest string `json:"scoped_request"`
}

type Router interface {
	Route(ctx context.Context, req RouteRequest) (RouteResult, error)
}

type modelCompleter interface {
	// Complete returns the model response, including any usage reported on error.
	Complete(ctx context.Context, req llm.Request) (llm.Response, error)
}

type llmRouter struct {
	chat      modelCompleter
	modelName string
}

func newLLMRouter(chat modelCompleter, modelName string) *llmRouter {
	return &llmRouter{chat: chat, modelName: strings.TrimSpace(modelName)}
}

func (r *llmRouter) Route(ctx context.Context, req RouteRequest) (RouteResult, error) {
	messages := make([]llm.Message, 0, len(req.RecentHistory)+routerExtraMessages)
	messages = append(messages, llm.Message{Role: "system", Content: routerSystemPrompt})
	for _, turn := range recentTurns(req.RecentHistory, routerHistoryTurns) {
		messages = append(messages, llm.Message{Role: turn.Role, Content: turn.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: strings.TrimSpace(req.Message)})

	completion, err := r.chat.Complete(ctx, llm.Request{
		Model: r.modelName, Temperature: 0, MaxTokens: routerMaxTokens, Messages: messages,
	})
	if err != nil {
		return RouteResult{Usage: completion.Usage}, fmt.Errorf("router completion: %w", err)
	}

	decision, err := decodeRouteDecision(completion.Content)
	if err != nil {
		return RouteResult{Usage: completion.Usage}, err
	}

	rawRoute := strings.ToLower(strings.TrimSpace(string(decision.Route)))
	route := Route(rawRoute)
	if localRouteMessageID(route) != "" {
		return RouteResult{Route: route, Usage: completion.Usage}, nil
	}
	if _, ok := routeProfiles[route]; !ok {
		if rawRoute != "" && !strings.ContainsAny(rawRoute, " \t\r\n") {
			return RouteResult{Usage: completion.Usage}, fmt.Errorf("%w: %q", errUnknownRoute, completion.Content)
		}
		return RouteResult{Usage: completion.Usage}, fmt.Errorf("%w: %q", errInvalidRouterResponse, completion.Content)
	}

	capabilities := decision.Capabilities
	if capabilities == nil {
		capabilities = defaultCapabilitiesForRoute(route)
	}
	normalizedCapabilities, ok := normalizeCapabilities(capabilities)
	if !ok {
		return RouteResult{Usage: completion.Usage}, fmt.Errorf(
			"%w: unknown capability in %q",
			errInvalidRouterResponse,
			completion.Content,
		)
	}

	operation := normalizeRouteOperation(route, decision.Operation, req.Message)
	if operation != nil {
		capability := operationCapability(operation.Type)
		if !hasCapability(normalizedCapabilities, capability) {
			normalizedCapabilities = append(normalizedCapabilities, capability)
		}
		if operation.Type == OperationSyncPauseSet && operation.Replicas != nil &&
			!hasCapability(normalizedCapabilities, CapabilityServiceOperations) {
			normalizedCapabilities = append(normalizedCapabilities, CapabilityServiceOperations)
		}
	}

	return RouteResult{
		Route:         route,
		Capabilities:  normalizedCapabilities,
		Operation:     operation,
		ScopedRequest: strings.TrimSpace(decision.ScopedRequest),
		Usage:         completion.Usage,
	}, nil
}

func normalizeRouteOperation(route Route, operation *OperationIntent, message string) *OperationIntent {
	if operation == nil || !operationAllowedForRoute(route, operation.Type) {
		return nil
	}
	if operation.Type == OperationServiceImageUpdate && !messageContainsImageTag(message, operation.ImageVersion) {
		operation.ImageVersion = ""
	}
	return operation
}

func messageContainsImageTag(message, tag string) bool {
	if tag == "" {
		return false
	}
	tokens := strings.FieldsFunc(message, func(r rune) bool {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == '-', r == '.':
			return false
		default:
			return true
		}
	})
	for _, token := range tokens {
		if token == tag || strings.TrimSuffix(token, ".") == tag {
			return true
		}
	}
	return false
}

func localRouteMessageID(route Route) string {
	switch route { //nolint:exhaustive // Only terminal routes have a local reply.
	case RouteGeneral, RouteOutOfScope:
		return "router.scope"
	case RouteNeedsClarification:
		return "router.clarify"
	default:
		return ""
	}
}

func decodeRouteDecision(content string) (RouteDecision, error) {
	raw := strings.TrimSpace(content)
	if !strings.HasPrefix(raw, "{") {
		return RouteDecision{Route: Route(strings.ToLower(raw))}, nil
	}
	var decision RouteDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return RouteDecision{}, fmt.Errorf("%w: %q", errInvalidRouterResponse, content)
	}
	return decision, nil
}

func operationCapability(operation OperationType) Capability {
	switch operation {
	case OperationDeploySync:
		return CapabilityDeploymentSync
	case OperationSyncPauseSet, OperationSyncPauseResume:
		return CapabilityPauseSync
	case OperationServiceRestart, OperationServiceReplicasSet, OperationServiceImageUpdate:
		return CapabilityServiceOperations
	default:
		return CapabilityServiceOperations
	}
}

func operationAllowedForRoute(route Route, operation OperationType) bool {
	switch operation {
	case OperationServiceRestart, OperationServiceReplicasSet, OperationServiceImageUpdate:
		return route == RouteServices
	case OperationDeploySync:
		return route == RouteDeployments
	case OperationSyncPauseSet, OperationSyncPauseResume:
		return route == RouteServices || route == RouteDeployments
	default:
		return false
	}
}

func routerFallbackReason(err error) string {
	switch {
	case errors.Is(err, errUnknownRoute):
		return "unknown_route"
	case errors.Is(err, errInvalidRouterResponse):
		return "invalid_response"
	default:
		return "router_error"
	}
}

func recentTurns(history []conversation.Turn, limit int) []conversation.Turn {
	if limit <= 0 || len(history) <= limit {
		return history
	}
	return history[len(history)-limit:]
}

//nolint:lll // Keeping prompt instructions on semantic lines makes the model-facing text easier to audit.
const routerSystemPrompt = `Classify the user's request for the swarm-deploy assistant.
Return compact JSON only: {"route":"<route>","capabilities":["<capability>"],"operation":null}.
First identify the requested outcome using the latest message and recent conversation. Supported goals are using swarm-deploy and deploying, configuring, observing, operating, or troubleshooting workloads and infrastructure. An infrastructure task can be supported without naming Docker or swarm-deploy.
Choose a response profile only when the requested result directly serves a supported goal or a listed lookup. Classify the outcome the user actually asks for. A related term, product name, or earlier infrastructure topic does not make a new task supported.
For a mixed request, choose the route, capabilities, and operation for its supported parts only. Add "scoped_request" containing those parts in the user's language, preserving exact resource names and relevant constraints. Omit it when the whole request is supported.
For general, out_of_scope, or needs_clarification, return empty capabilities and a null operation.
Capabilities are composable: include every context/tool group needed for the supported request.
Routes:
- general: greetings, acknowledgements, and questions about this assistant's identity or remit
- out_of_scope: clear requested outcome with no supported part
- needs_clarification: the requested outcome or its connection to a supported goal remains unclear after considering the conversation
- platform: how to use swarm-deploy and its built-in assistant, product behavior, running global configuration, and practical infrastructure guidance: installing components, monitoring, deployment patterns, and Docker Swarm concepts
- services: service catalog or service-focused inspection/operations
- cluster: nodes, Docker networks, plugins, or secrets
- deployments: synchronization, deployment/event history, recommendations, git history, or commit diffs
- diagnostics: investigation of failures or availability across runtime data sources
- lookups: focused DNS, TLS, registry, external release, date/time, or application metrics lookup
Capabilities:
- service_context: retrieve service.store metadata with service name, stack, image, description, type, and web routes
- service_runtime: read-only service health snapshot (tasks, open alerts, latest deployment), logs, specs, web-route ping, or dependencies
- service_operations: mutating service restart or replica changes
- cluster: nodes, Docker networks, plugins, and secrets
- deployment_history: desired-state deployments with nested attempts, user-facing event history, recommendations, local Git repository diagnostics, git history, and commit diffs
- alert_management: operational incidents and their open/resolved lifecycle
- deployment_sync: trigger synchronization
- pause_sync: inspect Pause Sync and create, change, or remove a pause through explicit confirmation
- registry: registry image version lookup
- external_release: upstream repository release lookup
- dns: DNS resolution
- tls: live certificate, chain, expiry, or negotiated TLS checks for a specific endpoint. Use lookups for a focused check and diagnostics when investigating a failure.
- metrics: application metrics lookup
- configuration: read actual global swarm-deploy settings when asked how it is configured, what is enabled, or what limits apply. Combine with runtime capabilities for diagnoses that depend on both settings and live evidence.
For supported explanations and how-to questions choose platform with empty capabilities and null operation unless the answer needs actual environment data.
Do not add capabilities merely because a route commonly uses them. Add only what this request needs.
Date/time uses the cross-cutting date tool and needs no capability.
For restart or replica changes, operation is {"type":"service_restart_trigger|service_replicas_set","target":"literal target or empty","replicas":number-or-null}; include service_operations.
For explicit requests to change a GitOps service image tag, use services with service_operations and operation {"type":"service_image_update","target":"literal target or empty","image_version":"literal tag or empty"}. Image version lookups use registry instead.
For a request to start synchronization, use the deployments route and operation {"type":"deploy_sync_trigger","target":"","replicas":null}; include deployment_sync.
For Pause Sync mutations, use operation {"type":"sync_pause_set|sync_pause_resume","target":"stack or stack/service","scope":"stack|service","expires_at":"absolute RFC3339 time or null","replicas":number-or-null}; include pause_sync. A replicas value combines pause-first with scaling.
Target is copied from the current user message. Never infer a missing operation target from history.
Examples:
"Привет" -> {"route":"general","capabilities":[],"operation":null}
"Где находится Юпитер?" -> {"route":"out_of_scope","capabilities":[],"operation":null}
"Покажи логи api" -> {"route":"services","capabilities":["service_context","service_runtime"],"operation":null}
"Проверь DNS api.example.com" -> {"route":"lookups","capabilities":["dns"],"operation":null}
"Когда истекает TLS-сертификат api.example.com?" -> {"route":"lookups","capabilities":["tls"],"operation":null}
"Why does TLS certificate validation fail on 10.0.0.15:8443?" -> {"route":"diagnostics","capabilities":["tls"],"operation":null}
"Проверь DNS у публичных урлов сервиса web-gateway-http" -> {"route":"lookups","capabilities":["service_context","dns"],"operation":null}
"Посмотри логи api и были ли перед этим деплои" -> {"route":"diagnostics","capabilities":["service_context","service_runtime","deployment_history"],"operation":null}
"Доступен ли Git-репозиторий?" -> {"route":"diagnostics","capabilities":["deployment_history","alert_management"],"operation":null}
"Как у меня настроены уведомления?" -> {"route":"platform","capabilities":["configuration"],"operation":null}
"Что включено в моём swarm-deploy?" -> {"route":"platform","capabilities":["configuration"],"operation":null}
"Сколько времени отведено init job?" -> {"route":"platform","capabilities":["configuration"],"operation":null}
"Что такое self-heal?" -> {"route":"platform","capabilities":[],"operation":null}
"Почему у payments не сработал self-heal?" -> {"route":"diagnostics","capabilities":["configuration","deployment_history","alert_management","pause_sync"],"operation":null}
"Какие деплои были вчера?" -> {"route":"deployments","capabilities":["deployment_history"],"operation":null}
"Почему упал деплой infra-monitoring?" -> {"route":"diagnostics","capabilities":["service_context","service_runtime","deployment_history","alert_management"],"operation":null}
"Какие сейчас активные алерты?" -> {"route":"diagnostics","capabilities":["alert_management"],"operation":null}
"Какие проблемы сейчас есть в кластере?" -> {"route":"cluster","capabilities":["cluster","alert_management"],"operation":null}
"Какой сегодня день?" -> {"route":"lookups","capabilities":[],"operation":null}
Use recent conversation to resolve follow-ups, including objections to the previous answer. Re-read the original user goal and consider that the previous assistant may have misunderstood it. Pending operation confirmation is handled by the backend.`
