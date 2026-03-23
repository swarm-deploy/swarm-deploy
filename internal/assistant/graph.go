package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/service"
)

const maxToolIterations = 3

type conversationTurn struct {
	role    string
	content string
}

type graph struct {
	config         Config
	guard          *promptGuard
	retriever      *retriever
	chat           *openAIClient
	tools          ToolExecutor
	allowedToolSet map[string]struct{}
}

func newGraph(
	config Config,
	guard *promptGuard,
	retriever *retriever,
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

func (g *graph) run(ctx context.Context, history []conversationTurn, userMessage string) (string, []ToolCall, error) {
	if err := g.guard.validate(userMessage); err != nil {
		return "", nil, err
	}

	relevantServices, err := g.retriever.retrieve(ctx, userMessage, defaultTopK)
	if err != nil {
		return "", nil, fmt.Errorf("retrieve context: %w", err)
	}

	messages := make([]modelMessage, 0, len(history)+4)
	messages = append(messages, modelMessage{
		Role:    "system",
		Content: buildSystemPrompt(g.config.SystemPrompt, g.allowedToolNames()),
	})

	if contextMessage := buildServicesContextMessage(relevantServices); contextMessage != "" {
		messages = append(messages, modelMessage{
			Role:    "system",
			Content: contextMessage,
		})
	}

	for _, turn := range history {
		messages = append(messages, modelMessage{
			Role:    turn.role,
			Content: turn.content,
		})
	}
	messages = append(messages, modelMessage{
		Role:    "user",
		Content: strings.TrimSpace(userMessage),
	})

	collectedToolCalls := make([]ToolCall, 0, 2)
	allowedToolDefinitions := g.allowedToolDefinitions()

	for i := 0; i < maxToolIterations; i++ {
		completion, completionErr := g.chat.complete(ctx, modelRequest{
			Model:       g.config.ModelName,
			Temperature: g.config.Temperature,
			MaxTokens:   g.config.MaxTokens,
			Messages:    messages,
			Tools:       allowedToolDefinitions,
		})
		if completionErr != nil {
			return "", collectedToolCalls, fmt.Errorf("chat completion: %w", completionErr)
		}

		if len(completion.ToolCalls) == 0 {
			return strings.TrimSpace(completion.Content), collectedToolCalls, nil
		}

		messages = append(messages, modelMessage{
			Role:      "assistant",
			Content:   completion.Content,
			ToolCalls: completion.ToolCalls,
		})

		for _, modelToolCall := range completion.ToolCalls {
			toolCallInfo := ToolCall{
				Name:      modelToolCall.Name,
				Arguments: modelToolCall.Arguments,
			}

			toolResultMessage := ""
			if !g.isToolAllowed(modelToolCall.Name) {
				toolCallInfo.Error = "tool is not allowed by assistant.tools configuration"
				toolResultMessage = toolCallInfo.Error
			} else {
				arguments, decodeErr := decodeToolArguments(modelToolCall.Arguments)
				if decodeErr != nil {
					toolCallInfo.Error = decodeErr.Error()
					toolResultMessage = decodeErr.Error()
				} else {
					result, runErr := g.tools.Execute(ctx, modelToolCall.Name, arguments)
					if runErr != nil {
						toolCallInfo.Error = runErr.Error()
						toolResultMessage = runErr.Error()
					} else {
						toolCallInfo.Result = result
						toolResultMessage = result
					}
				}
			}

			collectedToolCalls = append(collectedToolCalls, toolCallInfo)
			messages = append(messages, modelMessage{
				Role:       "tool",
				Name:       modelToolCall.Name,
				ToolCallID: modelToolCall.ID,
				Content:    strings.TrimSpace(toolResultMessage),
			})
		}
	}

	return "", collectedToolCalls, fmt.Errorf("tool iteration limit exceeded")
}

func (g *graph) allowedToolDefinitions() []ToolDefinition {
	definitions := g.tools.Definitions()
	if len(g.allowedToolSet) == 0 {
		return definitions
	}

	filtered := make([]ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if g.isToolAllowed(definition.Name) {
			filtered = append(filtered, definition)
		}
	}

	return filtered
}

func (g *graph) allowedToolNames() []string {
	definitions := g.allowedToolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}

	return names
}

func (g *graph) isToolAllowed(toolName string) bool {
	if len(g.allowedToolSet) == 0 {
		return true
	}

	_, ok := g.allowedToolSet[toolName]
	return ok
}

func decodeToolArguments(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	if decoded == nil {
		return map[string]any{}, nil
	}

	return decoded, nil
}

func buildServicesContextMessage(services []service.Info) string {
	if len(services) == 0 {
		return "No service metadata is available in service.store."
	}

	builder := strings.Builder{}
	builder.WriteString("Relevant service metadata from service.store:\n")
	for _, serviceInfo := range services {
		builder.WriteString("- ")
		builder.WriteString(serviceToDocument(serviceInfo))
		builder.WriteByte('\n')
	}

	return strings.TrimSpace(builder.String())
}
