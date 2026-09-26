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

// Route identifies a bounded assistant capability group.
type Route string

const (
	// RouteGeneral handles short conversational interaction with the assistant itself.
	RouteGeneral Route = "general"
	// RouteOutOfScope handles requests outside swarm-deploy and its operational domain.
	RouteOutOfScope Route = "out_of_scope"
	// RoutePlatform handles questions about swarm-deploy itself.
	RoutePlatform Route = "platform"
	// RouteServices handles service inspection and service operations.
	RouteServices Route = "services"
	// RouteCluster handles Swarm cluster resources.
	RouteCluster Route = "cluster"
	// RouteDeployments handles synchronization and repository deployment history.
	RouteDeployments Route = "deployments"
	// RouteDiagnostics handles investigations across runtime data sources.
	RouteDiagnostics Route = "diagnostics"
	// RouteLookups handles focused external or utility lookups.
	RouteLookups Route = "lookups"
)

var (
	errInvalidRouterResponse = errors.New("invalid router response")
	errUnknownRoute          = errors.New("unknown route")
)

// RouteRequest contains the minimum conversation context needed for routing.
type RouteRequest struct {
	// Message is the current user message.
	Message string
	// RecentHistory contains the most recent conversation turns.
	RecentHistory []conversation.Turn
}

// RouteResult contains the selected route and the model usage incurred while selecting it.
type RouteResult struct {
	// Route is the selected capability route.
	Route Route
	// Operation contains a supported mutating operation recognized by the router.
	Operation *OperationIntent
	// Usage is token usage reported by the router completion.
	Usage conversation.TokenUsage
}

// Router classifies assistant requests without receiving tools or retrieved context.
type Router interface {
	// Route selects one capability route for the request.
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
	return &llmRouter{
		chat:      chat,
		modelName: strings.TrimSpace(modelName),
	}
}

func (r *llmRouter) Route(ctx context.Context, req RouteRequest) (RouteResult, error) {
	messages := make([]modelMessage, 0, len(req.RecentHistory)+routerExtraMessages)
	messages = append(messages, modelMessage{Role: "system", Content: routerSystemPrompt})
	for _, turn := range recentTurns(req.RecentHistory, routerHistoryTurns) {
		messages = append(messages, modelMessage{
			Role:    turn.Role,
			Content: turn.Content,
		})
	}
	messages = append(messages, modelMessage{
		Role:    "user",
		Content: strings.TrimSpace(req.Message),
	})

	completion, err := r.chat.complete(ctx, modelRequest{
		Model:       r.modelName,
		Temperature: 0,
		MaxTokens:   routerMaxTokens,
		Messages:    messages,
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("router completion: %w", err)
	}

	rawContent := strings.TrimSpace(completion.Content)
	var decision RouteDecision
	if strings.HasPrefix(rawContent, "{") {
		if err := json.Unmarshal([]byte(rawContent), &decision); err != nil {
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

	if decision.Operation != nil && (route != RouteServices || !decision.Operation.Type.supported()) {
		decision.Operation = nil
	}

	return RouteResult{Route: route, Operation: decision.Operation, Usage: completion.Usage}, nil
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
	_, ok := capabilityProfiles[route]
	return ok
}

const routerSystemPrompt = `Classify the user's request for the swarm-deploy assistant and extract supported mutating operations.
The assistant is exclusively for swarm-deploy and closely related Docker Swarm, deployment, runtime, observability, troubleshooting, and infrastructure operations.
Return compact JSON only: {"route":"<route>","operation":null}.
For restart or replica changes, operation is {"type":"service_restart_trigger|service_replicas_set","stack":"candidate or empty","service":"candidate or empty","replicas":number-or-null}.
Candidate values come only from the current user message. Never infer missing identifiers from history.
Routes:
- general: only greetings, acknowledgements, assistant identity, and short conversational interactions with the assistant itself
- out_of_scope: questions or requests unrelated to the assistant's operational domain; do not use general for general-knowledge or creative requests
- platform: questions about swarm-deploy capabilities or behavior, without runtime inspection
- services: service catalog, logs, specs, images, replicas, restart, routes, or dependencies
- cluster: nodes, Docker networks, plugins, or secrets
- deployments: sync, deployment/event history, recommendations, git history, or commit diffs
- diagnostics: investigating failures, availability, or runtime problems using multiple data sources
- lookups: focused registry, external release, DNS, date/time, or application metrics lookup
Examples:
"Привет" -> {"route":"general","operation":null}; "Где находится Юпитер?" -> {"route":"out_of_scope","operation":null}
"Как приготовить борщ?" -> {"route":"out_of_scope","operation":null}; "Почему api падает?" -> {"route":"diagnostics","operation":null}
Legacy route examples: "Где находится Юпитер?" -> out_of_scope; "Как приготовить борщ?" -> out_of_scope
Use recent history only to select the route for ordinary follow-ups. Pending operation confirmation is handled by the backend.`
