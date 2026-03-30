package assistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/assistant/conversation"
	"github.com/artarts36/swarm-deploy/internal/assistant/guard"
	"github.com/artarts36/swarm-deploy/internal/assistant/rag"
	"github.com/artarts36/swarm-deploy/internal/service"
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
	graphNodeRetrievePlan     = "retrieve_plan"
	graphNodeRetrieveLexical  = "retrieve_lexical"
	graphNodeRetrieveSemantic = "retrieve_semantic"
	graphNodePrepare          = "prepare_messages"
	graphNodeGenerateAnswer   = "generate_answer"
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

type graph struct {
	config         Config
	guard          *guard.InjectionChecker
	retriever      *rag.Retriever
	chat           *openAIClient
	tools          ToolExecutor
	allowedToolSet map[string]struct{}
}

type graphExecutionState struct {
	history          []conversation.Turn
	userMessage      string
	retrievalPlan    *rag.RetrievalPlan
	relevantServices []service.Info
	modelMessages    []modelMessage
	answer           string
}

func newGraph(
	config Config,
	guard *guard.InjectionChecker,
	retriever *rag.Retriever,
	chat *openAIClient,
	tools ToolExecutor,
	allowedToolSet map[string]struct{},
) *graph {
	return &graph{
		config:         config,
		guard:          guard,
		retriever:      retriever,
		chat:           chat,
		tools:          tools,
		allowedToolSet: allowedToolSet,
	}
}

func (g *graph) run(ctx context.Context, history []conversation.Turn, userMessage string) (string, error) {
	executionState := &graphExecutionState{
		history:     history,
		userMessage: userMessage,
	}

	runnable, err := g.compile(executionState)
	if err != nil {
		return "", err
	}

	if _, invokeErr := runnable.Invoke(ctx, nil); invokeErr != nil {
		return "", invokeErr
	}

	return executionState.answer, nil
}

func (g *graph) compile(executionState *graphExecutionState) (*langgraph.Runnable, error) {
	messageGraph := langgraph.NewMessageGraph()

	messageGraph.AddNode(graphNodeGuard, g.guardNode(executionState))
	messageGraph.AddNode(graphNodeRetrievePlan, g.retrievePlanNode(executionState))
	messageGraph.AddNode(graphNodeRetrieveLexical, g.retrieveLexicalNode(executionState))
	messageGraph.AddNode(graphNodeRetrieveSemantic, g.retrieveSemanticNode(executionState))
	messageGraph.AddNode(graphNodePrepare, g.prepareNode(executionState))
	messageGraph.AddNode(graphNodeGenerateAnswer, g.generateAnswerNode(executionState))

	messageGraph.AddConditionalEdges(
		graphNodeGuard,
		func(_ context.Context, _ []llms.MessageContent) string {
			if shouldSkipContextRetrieval(executionState.userMessage) {
				return graphNodePrepare
			}

			return graphNodeRetrievePlan
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
	messageGraph.AddEdge(graphNodeGenerateAnswer, langgraph.END)
	messageGraph.SetEntryPoint(graphNodeGuard)

	return messageGraph.Compile()
}

func (g *graph) guardNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	return func(_ context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		if hasInjections := g.guard.Check(executionState.userMessage); hasInjections {
			return messages, errPromptInjection
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
		messages = append(messages, modelMessage{
			Role:    "system",
			Content: buildSystemPrompt(g.config.SystemPrompt, g.allowedToolNames()),
		})

		if contextMessage := buildServicesContextMessage(executionState.relevantServices); contextMessage != "" {
			messages = append(messages, modelMessage{
				Role:    "system",
				Content: contextMessage,
			})
		}

		for _, turn := range executionState.history {
			messages = append(messages, modelMessage{
				Role:    turn.Role,
				Content: turn.Content,
			})
		}
		messages = append(messages, modelMessage{
			Role:    "user",
			Content: strings.TrimSpace(executionState.userMessage),
		})

		executionState.modelMessages = messages
		return state, nil
	}
}

func (g *graph) generateAnswerNode(
	executionState *graphExecutionState,
) func(context.Context, []llms.MessageContent) ([]llms.MessageContent, error) {
	allowedToolDefinitions := g.allowedToolDefinitions()

	return func(ctx context.Context, messages []llms.MessageContent) ([]llms.MessageContent, error) {
		for i := 0; i < maxToolIterations; i++ {
			completion, completionErr := g.chat.complete(ctx, modelRequest{
				Model:       g.config.ModelName,
				Temperature: g.config.Temperature,
				MaxTokens:   g.config.MaxTokens,
				Messages:    executionState.modelMessages,
				Tools:       allowedToolDefinitions,
			})
			if completionErr != nil {
				return messages, fmt.Errorf("chat completion: %w", completionErr)
			}

			if len(completion.ToolCalls) == 0 {
				executionState.answer = strings.TrimSpace(completion.Content)
				return messages, nil
			}

			executionState.modelMessages = append(executionState.modelMessages, modelMessage{
				Role:      "assistant",
				Content:   completion.Content,
				ToolCalls: completion.ToolCalls,
			})

			for _, modelToolCall := range completion.ToolCalls {
				slog.InfoContext(ctx, "[graph] running mcp tool", slog.String("tool.name", modelToolCall.Name))

				toolResultMessage, err := g.executeToolCall(ctx, modelToolCall)
				if err != nil {
					slog.ErrorContext(ctx, "[graph] failed to run mcp tool",
						slog.String("tool.name", modelToolCall.Name),
						slog.Any("err", err),
					)
				}

				executionState.modelMessages = append(executionState.modelMessages, modelMessage{
					Role:       "tool",
					Name:       modelToolCall.Name,
					ToolCallID: modelToolCall.ID,
					Content:    strings.TrimSpace(toolResultMessage),
				})
			}
		}

		return messages, fmt.Errorf("tool iteration limit exceeded")
	}
}

func shouldSkipContextRetrieval(userMessage string) bool {
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
