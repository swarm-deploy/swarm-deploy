package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/assistant/conversation"
	"github.com/swarm-deploy/swarm-deploy/internal/assistant/guard"
	"github.com/swarm-deploy/swarm-deploy/internal/assistant/rag"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
	"github.com/tmc/langchaingo/llms"
	langgraph "github.com/tmc/langgraphgo/graph"
)

const (
	maxToolIterations            = 3
	prepareMessagesExtraCapacity = 4
	servicesContextMaxRows       = 64
)

const (
	graphNodeGuard            = "guard"
	graphNodeRoute            = "route"
	graphNodeRetrievePlan     = "retrieve_plan"
	graphNodeRetrieveLexical  = "retrieve_lexical"
	graphNodeRetrieveSemantic = "retrieve_semantic"
	graphNodePrepare          = "prepare_messages"
	graphNodeGenerateAnswer   = "generate_answer"
	graphNodeExecuteMCP       = "execute_mcp"
	graphNodeGuardMCPResults  = "guard_mcp_results"
	graphNodeFinalizeTerminal = "finalize_terminal_result"
)

var helloMessages = map[string]struct{}{
	"hello":       {},
	"hi":          {},
	"hey":         {},
	"thanks":      {},
	"thank you":   {},
	"привет":      {},
	"здарова":     {},
	"ку":          {},
	"здравствуй":  {},
	"добрый день": {},
	"спасибо":     {},
}

var (
	errPromptInjection = errors.New("request rejected by prompt injection guard")
)

type promptInjectionError struct {
	prompt string
}

type routerFallbackObserver interface {
	// RecordRouterFallback tracks why the graph selected its fallback capability profile.
	RecordRouterFallback(reason string)
}

func (e *promptInjectionError) Error() string {
	return errPromptInjection.Error()
}

func (e *promptInjectionError) Unwrap() error {
	return errPromptInjection
}

type graph struct {
	config         Config
	guard          *guard.InjectionChecker
	retriever      *rag.Retriever
	chat           modelCompleter
	router         Router
	tools          ToolExecutor
	allowedToolSet map[string]struct{}
	observer       routerFallbackObserver
}

type graphExecutionState struct {
	history          []conversation.Turn
	userMessage      string
	route            Route
	profile          CapabilityProfile
	effectiveToolSet map[string]struct{}
	retrievalPlan    *rag.RetrievalPlan
	relevantServices []service.Info
	modelMessages    []modelMessage
	pendingToolCalls []modelToolCall
	lastToolResults  []toolExecutionResult
	toolIterations   int
	answer           string
	usage            conversation.TokenUsage
	rejectedPrompt   string
	preparedSizes    preparedRequestSizes
	terminalTool     string
}

type toolExecutionResult struct {
	toolName string
	content  string
	success  bool
}

func newGraph(
	config Config,
	guard *guard.InjectionChecker,
	retriever *rag.Retriever,
	chat modelCompleter,
	router Router,
	tools ToolExecutor,
	allowedToolSet map[string]struct{},
	observer routerFallbackObserver,
) *graph {
	return &graph{
		config:         config,
		guard:          guard,
		retriever:      retriever,
		chat:           chat,
		router:         router,
		tools:          tools,
		allowedToolSet: allowedToolSet,
		observer:       observer,
	}
}

func (g *graph) run(
	ctx context.Context,
	history []conversation.Turn,
	userMessage string,
) (string, conversation.TokenUsage, error) {
	executionState := &graphExecutionState{
		history:     history,
		userMessage: userMessage,
	}

	runnable, err := g.compile(executionState)
	if err != nil {
		return "", executionState.usage, err
	}

	if _, invokeErr := runnable.Invoke(ctx, nil); invokeErr != nil {
		if errors.Is(invokeErr, errPromptInjection) {
			return "", executionState.usage, &promptInjectionError{
				prompt: strings.TrimSpace(executionState.rejectedPrompt),
			}
		}

		return "", executionState.usage, invokeErr
	}

	return executionState.answer, executionState.usage, nil
}

func (g *graph) compile(executionState *graphExecutionState) (*langgraph.Runnable, error) {
	messageGraph := langgraph.NewMessageGraph()

	messageGraph.AddNode(graphNodeGuard, g.guardNode(executionState))
	messageGraph.AddNode(graphNodeRoute, g.routeNode(executionState))
	messageGraph.AddNode(graphNodeRetrievePlan, g.retrievePlanNode(executionState))
	messageGraph.AddNode(graphNodeRetrieveLexical, g.retrieveLexicalNode(executionState))
	messageGraph.AddNode(graphNodeRetrieveSemantic, g.retrieveSemanticNode(executionState))
	messageGraph.AddNode(graphNodePrepare, g.prepareNode(executionState))
	messageGraph.AddNode(graphNodeGenerateAnswer, g.generateAnswerNode(executionState))
	messageGraph.AddNode(graphNodeExecuteMCP, g.executeMCPNode(executionState))
	messageGraph.AddNode(graphNodeGuardMCPResults, g.guardMCPResultsNode(executionState))
	messageGraph.AddNode(graphNodeFinalizeTerminal, g.finalizeTerminalResultNode(executionState))

	messageGraph.AddEdge(graphNodeGuard, graphNodeRoute)
	messageGraph.AddConditionalEdges(
		graphNodeRoute,
		func(_ context.Context, _ []llms.MessageContent) string {
			if executionState.profile.ServiceContext {
				return graphNodeRetrievePlan
			}

			return graphNodePrepare
		},
		map[string]string{
			graphNodePrepare:      graphNodePrepare,
			graphNodeRetrievePlan: graphNodeRetrievePlan,
		},
	)
	messageGraph.AddConditionalEdges(
		graphNodeRetrievePlan,
		func(_ context.Context, _ []llms.MessageContent) string {
			switch executionState.retrievalPlan.Branch() {
			case rag.RetrievalPlanBranchNone:
				return graphNodePrepare
			case rag.RetrievalPlanBranchLexical:
				return graphNodeRetrieveLexical
			case rag.RetrievalPlanBranchSemantic:
				return graphNodeRetrieveSemantic
			default:
				return graphNodePrepare
			}
		},
		map[string]string{
			graphNodePrepare:          graphNodePrepare,
			graphNodeRetrieveLexical:  graphNodeRetrieveLexical,
			graphNodeRetrieveSemantic: graphNodeRetrieveSemantic,
		},
	)
	messageGraph.AddEdge(graphNodeRetrieveLexical, graphNodePrepare)
	messageGraph.AddEdge(graphNodeRetrieveSemantic, graphNodePrepare)
	messageGraph.AddEdge(graphNodePrepare, graphNodeGenerateAnswer)
	messageGraph.AddConditionalEdges(
		graphNodeGenerateAnswer,
		func(_ context.Context, _ []llms.MessageContent) string {
			if len(executionState.pendingToolCalls) == 0 {
				return langgraph.END
			}

			return graphNodeExecuteMCP
		},
		map[string]string{
			langgraph.END:       langgraph.END,
			graphNodeExecuteMCP: graphNodeExecuteMCP,
		},
	)
	messageGraph.AddEdge(graphNodeExecuteMCP, graphNodeGuardMCPResults)
	messageGraph.AddConditionalEdges(
		graphNodeGuardMCPResults,
		func(_ context.Context, _ []llms.MessageContent) string {
			if isTerminalToolResultCandidate(executionState.lastToolResults) {
				return graphNodeFinalizeTerminal
			}

			return graphNodeGenerateAnswer
		},
		map[string]string{
			graphNodeFinalizeTerminal: graphNodeFinalizeTerminal,
			graphNodeGenerateAnswer:   graphNodeGenerateAnswer,
		},
	)
	messageGraph.AddConditionalEdges(
		graphNodeFinalizeTerminal,
		func(_ context.Context, _ []llms.MessageContent) string {
			if executionState.terminalTool != "" {
				return langgraph.END
			}

			return graphNodeGenerateAnswer
		},
		map[string]string{
			langgraph.END:           langgraph.END,
			graphNodeGenerateAnswer: graphNodeGenerateAnswer,
		},
	)
	messageGraph.SetEntryPoint(graphNodeGuard)

	return messageGraph.Compile()
}

func (g *graph) routeNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		route := RouteGeneral
		if !isGreeting(executionState.userMessage) {
			result, err := g.router.Route(ctx, RouteRequest{
				Message:       executionState.userMessage,
				RecentHistory: executionState.history,
			})
			executionState.usage.Add(result.Usage)
			if err != nil {
				reason := routerFallbackReason(err)
				slog.WarnContext(ctx, "[assistant] router fallback",
					slog.String("reason", reason),
					slog.Any("err", err),
				)
				if g.observer != nil {
					g.observer.RecordRouterFallback(reason)
				}
				executionState.profile = fallbackCapabilityProfile(g.builtInToolNames())
				executionState.effectiveToolSet = g.effectiveToolSet(executionState.profile)
				return messages, nil
			}
			route = result.Route
		}

		profile, ok := capabilityProfile(route)
		if !ok {
			return messages, fmt.Errorf("capability profile for route %q is not configured", route)
		}
		executionState.route = route
		executionState.profile = profile
		executionState.effectiveToolSet = g.effectiveToolSet(profile)
		return messages, nil
	}
}

func (g *graph) guardNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		if hasInjections := g.guard.Check(executionState.userMessage); hasInjections {
			executionState.rejectedPrompt = executionState.userMessage
			return messages, &promptInjectionError{
				prompt: strings.TrimSpace(executionState.rejectedPrompt),
			}
		}

		return messages, nil
	}
}

func (g *graph) retrievePlanNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		plan, err := g.retriever.Plan(ctx, executionState.userMessage)
		if err != nil {
			return messages, fmt.Errorf("retrieve plan: %w", err)
		}

		executionState.retrievalPlan = plan
		return messages, nil
	}
}

func (g *graph) retrieveLexicalNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		relevantServices, err := g.retriever.RetrieveLexical(executionState.retrievalPlan)
		if err != nil {
			return messages, fmt.Errorf("retrieve lexical context: %w", err)
		}

		executionState.relevantServices = relevantServices
		return messages, nil
	}
}

func (g *graph) retrieveSemanticNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		relevantServices, err := g.retriever.RetrieveSemantic(executionState.retrievalPlan)
		if err != nil {
			return messages, fmt.Errorf("retrieve semantic context: %w", err)
		}

		executionState.relevantServices = relevantServices
		return messages, nil
	}
}

func (g *graph) prepareNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, state []llms.MessageContent) ([]llms.MessageContent, error) {
		messages := make([]modelMessage, 0, len(executionState.history)+prepareMessagesExtraCapacity)
		systemPrompt := buildSystemPrompt(g.config.SystemPrompt, executionState.profile.Prompt)
		messages = append(messages, modelMessage{
			Role:    "system",
			Content: systemPrompt,
		})

		contextChars := 0
		if executionState.profile.ServiceContext {
			if contextMessage := buildServicesContextMessage(executionState.relevantServices); contextMessage != "" {
				contextChars = textChars(contextMessage)
				messages = append(messages, modelMessage{
					Role:    "system",
					Content: contextMessage,
				})
			}
		}

		historyChars := 0
		for _, turn := range executionState.history {
			historyChars += textChars(turn.Content)
			messages = append(messages, modelMessage{
				Role:    turn.Role,
				Content: turn.Content,
			})
		}
		userMessage := strings.TrimSpace(executionState.userMessage)
		messages = append(messages, modelMessage{
			Role:    "user",
			Content: userMessage,
		})

		executionState.modelMessages = messages
		executionState.preparedSizes = preparedRequestSizes{
			systemPromptChars: textChars(systemPrompt),
			historyChars:      historyChars,
			contextChars:      contextChars,
			userMessageChars:  textChars(userMessage),
			messageCount:      len(messages),
		}
		return state, nil
	}
}

func (g *graph) generateAnswerNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		if executionState.toolIterations >= maxToolIterations {
			return messages, fmt.Errorf("tool iteration limit exceeded")
		}

		request := modelRequest{
			Model:       g.config.ModelName,
			Temperature: g.config.Temperature,
			MaxTokens:   g.config.MaxTokens,
			Messages:    executionState.modelMessages,
			Tools:       g.effectiveToolDefinitions(executionState.effectiveToolSet),
		}
		recordModelRequestDiagnostics(
			ctx,
			executionState.route,
			calculateModelRequestDiagnostics(request, executionState.preparedSizes),
		)

		completion, completionErr := g.chat.complete(ctx, request)
		if completionErr != nil {
			return messages, fmt.Errorf("chat completion: %w", completionErr)
		}
		executionState.usage.Add(completion.Usage)

		if len(completion.ToolCalls) == 0 {
			executionState.answer = strings.TrimSpace(completion.Content)
			executionState.pendingToolCalls = nil
			return messages, nil
		}

		executionState.modelMessages = append(executionState.modelMessages, modelMessage{
			Role:      "assistant",
			Content:   completion.Content,
			ToolCalls: completion.ToolCalls,
		})
		executionState.pendingToolCalls = completion.ToolCalls
		executionState.toolIterations++

		return messages, nil
	}
}

func (g *graph) executeMCPNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		executionState.lastToolResults = executionState.lastToolResults[:0]
		for _, modelToolCall := range executionState.pendingToolCalls {
			slog.InfoContext(ctx, "[graph] running mcp tool", slog.String("tool.name", modelToolCall.Name))

			toolResultMessage, err := g.executeToolCall(ctx, modelToolCall, executionState.effectiveToolSet)
			success := err == nil
			if err != nil {
				slog.ErrorContext(ctx, "[graph] failed to run mcp tool",
					slog.String("tool.name", modelToolCall.Name),
					slog.Any("err", err),
				)
				toolResultMessage = formatMCPToolCallError(modelToolCall.Name, err)
			}

			if strings.TrimSpace(toolResultMessage) == "" {
				toolResultMessage = "MCP tool returned empty content."
			}

			executionState.modelMessages = append(executionState.modelMessages, modelMessage{
				Role:       "tool",
				Name:       modelToolCall.Name,
				ToolCallID: modelToolCall.ID,
				Content:    strings.TrimSpace(toolResultMessage),
			})
			executionState.lastToolResults = append(executionState.lastToolResults, toolExecutionResult{
				toolName: modelToolCall.Name,
				content:  strings.TrimSpace(toolResultMessage),
				success:  success,
			})
		}

		executionState.pendingToolCalls = nil
		return messages, nil
	}
}

func (g *graph) finalizeTerminalResultNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		if !isTerminalToolResultCandidate(executionState.lastToolResults) {
			return messages, nil
		}

		result := executionState.lastToolResults[0]
		answer, err := finalizeTerminalToolResult(result.toolName, result.content)
		if err != nil {
			slog.ErrorContext(ctx, "[assistant] failed to finalize terminal tool result, falling back to generation",
				slog.String("tool.name", result.toolName),
				slog.Any("err", err),
			)
			return messages, nil
		}

		executionState.answer = answer
		executionState.terminalTool = result.toolName
		recordTerminalTool(ctx, result.toolName)
		return messages, nil
	}
}

func (g *graph) guardMCPResultsNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		for _, toolResult := range executionState.lastToolResults {
			if !g.guard.Check(toolResult.content) {
				continue
			}

			executionState.rejectedPrompt = fmt.Sprintf(
				"tool %q result contained prompt injection markers",
				toolResult.toolName,
			)
			return messages, &promptInjectionError{
				prompt: strings.TrimSpace(executionState.rejectedPrompt),
			}
		}

		return messages, nil
	}
}

func isGreeting(userMessage string) bool {
	normalized := strings.ToLower(strings.TrimSpace(userMessage))
	if normalized == "" {
		return true
	}

	_, ok := helloMessages[normalized]
	return ok
}

func buildServicesContextMessage(services []service.Info) string {
	if len(services) == 0 {
		return "No service metadata is available in service.store."
	}

	documentBuilder := rag.NewServiceDocumentBuilder()
	builder := strings.Builder{}
	builder.WriteString("Relevant service metadata from service.store (RAG retrieval, sorted by relevance):\n")
	limited := services
	if len(limited) > servicesContextMaxRows {
		limited = limited[:servicesContextMaxRows]
	}
	for _, serviceInfo := range limited {
		builder.WriteString("- ")
		builder.WriteString(documentBuilder.Build(serviceInfo))
		builder.WriteByte('\n')
	}
	if len(limited) < len(services) {
		fmt.Fprintf(&builder, "- ... and %d more services\n", len(services)-len(limited))
	}

	return strings.TrimSpace(builder.String())
}
