package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant/conversation"
)

const (
	routerMaxTokens     = 128
	routerHistoryTurns  = 6
	routerExtraMessages = 2
)

type Route string

const (
	RouteGeneral     Route = "general"
	RouteOutOfScope  Route = "out_of_scope"
	RoutePlatform    Route = "platform"
	RouteServices    Route = "services"
	RouteCluster     Route = "cluster"
	RouteDeployments Route = "deployments"
	RouteDiagnostics Route = "diagnostics"
	RouteLookups     Route = "lookups"
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
	Route        Route
	Capabilities []Capability
	Operation    *OperationIntent
	Usage        conversation.TokenUsage
}

type Router interface {
	Route(ctx context.Context, req RouteRequest) (RouteResult, error)
}

type modelCompleter interface {
	complete(ctx context.Context, req modelRequest) (modelResponse, error)
}

type llmRouter struct {
	chat      modelCompleter
	modelName string
}

func newLLMRouter(chat modelCompleter, modelName string) *llmRouter {
	return &llmRouter{chat: chat, modelName: strings.TrimSpace(modelName)}
}

func (r *llmRouter) Route(ctx context.Context, req RouteRequest) (RouteResult, error) {
	messages := make([]modelMessage, 0, len(req.RecentHistory)+routerExtraMessages)
	messages = append(messages, modelMessage{Role: "system", Content: routerSystemPrompt})
	for _, turn := range recentTurns(req.RecentHistory, routerHistoryTurns) {
		messages = append(messages, modelMessage{Role: turn.Role, Content: turn.Content})
	}
	messages = append(messages, modelMessage{Role: "user", Content: strings.TrimSpace(req.Message)})

	completion, err := r.chat.complete(ctx, modelRequest{
		Model: r.modelName, Temperature: 0, MaxTokens: routerMaxTokens, Messages: messages,
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("router completion: %w", err)
	}

	rawContent := strings.TrimSpace(completion.Content)
	var decision RouteDecision
	if strings.HasPrefix(rawContent, "{") {
		if unmarshalErr := json.Unmarshal([]byte(rawContent), &decision); unmarshalErr != nil {
			return RouteResult{Usage: completion.Usage}, fmt.Errorf("%w: %q", errInvalidRouterResponse, completion.Content)
		}
	} else {
		decision.Route = Route(strings.ToLower(rawContent))
	}

	rawRoute := strings.ToLower(strings.TrimSpace(string(decision.Route)))
	route := Route(rawRoute)
	if !isKnownRoute(route) {
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
		return RouteResult{Usage: completion.Usage}, fmt.Errorf("%w: unknown capability in %q", errInvalidRouterResponse, completion.Content)
	}

	if decision.Operation != nil && (route != RouteServices || !decision.Operation.Type.supported()) {
		decision.Operation = nil
	}
	if decision.Operation != nil && !hasCapability(normalizedCapabilities, CapabilityServiceRuntime) {
		normalizedCapabilities = append(normalizedCapabilities, CapabilityServiceRuntime)
	}

	return RouteResult{
		Route: route, Capabilities: normalizedCapabilities, Operation: decision.Operation, Usage: completion.Usage,
	}, nil
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

func isKnownRoute(route Route) bool {
	_, ok := routeProfiles[route]
	return ok
}

//nolint:lll // Keeping prompt instructions on semantic lines makes the model-facing text easier to audit.
const routerSystemPrompt = `Classify the user's request for the swarm-deploy assistant.
Return compact JSON only: {"route":"<route>","capabilities":["<capability>"],"operation":null}.
Choose exactly one primary route for response guidance. Capabilities are composable: include every context/tool group needed to complete the request, including cross-domain requests.
Routes:
- general: greetings, acknowledgements, assistant identity, and short conversation with the assistant itself
- out_of_scope: unrelated to swarm-deploy, Docker Swarm, deployment, runtime, observability, troubleshooting, or infrastructure operations
- platform: questions about swarm-deploy capabilities or behavior without runtime inspection
- services: service catalog or service-focused inspection/operations
- cluster: nodes, Docker networks, plugins, or secrets
- deployments: synchronization, deployment/event history, recommendations, git history, or commit diffs
- diagnostics: investigation of failures or availability across runtime data sources
- lookups: focused DNS, registry, external release, date/time, or application metrics lookup
Capabilities:
- service_context: retrieve service.store metadata with service name, stack, image, description, type, and web routes
- service_runtime: service logs, specs, replicas, restart, web-route ping, or dependencies
- cluster: nodes, Docker networks, plugins, and secrets
- deployment_history: events, recommendations, git history, and commit diffs
- deployment_sync: trigger synchronization
- registry: registry image version lookup
- external_release: upstream repository release lookup
- dns: DNS resolution
- metrics: application metrics lookup
Do not add capabilities merely because a route commonly uses them. Add only what this request needs.
Date/time uses the cross-cutting date tool and needs no capability.
For restart or replica changes, operation is {"type":"service_restart_trigger|service_replicas_set","target":"literal target or empty","replicas":number-or-null}; include service_runtime.
Target is copied from the current user message. Never infer a missing operation target from history.
Examples:
"Привет" -> {"route":"general","capabilities":[],"operation":null}
"Где находится Юпитер?" -> {"route":"out_of_scope","capabilities":[],"operation":null}
"Покажи логи api" -> {"route":"services","capabilities":["service_context","service_runtime"],"operation":null}
"Проверь DNS api.example.com" -> {"route":"lookups","capabilities":["dns"],"operation":null}
"Проверь DNS у публичных урлов сервиса web-gateway-http" -> {"route":"lookups","capabilities":["service_context","dns"],"operation":null}
"Посмотри логи api и были ли перед этим деплои" -> {"route":"diagnostics","capabilities":["service_context","service_runtime","deployment_history"],"operation":null}
"Какие деплои были вчера?" -> {"route":"deployments","capabilities":["deployment_history"],"operation":null}
"Какой сегодня день?" -> {"route":"lookups","capabilities":[],"operation":null}
Use recent history only to resolve ordinary follow-up intent. Pending operation confirmation is handled by the backend.`
