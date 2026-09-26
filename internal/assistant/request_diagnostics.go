package assistant

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type preparedRequestSizes struct {
	systemPromptChars int
	historyChars      int
	contextChars      int
	userMessageChars  int
	messageCount      int
}

type modelRequestDiagnostics struct {
	systemPromptChars int
	historyChars      int
	contextChars      int
	userMessageChars  int
	toolsChars        int
	messageCount      int
	toolCount         int
}

func calculateModelRequestDiagnostics(
	req modelRequest,
	prepared preparedRequestSizes,
) modelRequestDiagnostics {
	historyChars := prepared.historyChars
	if prepared.messageCount < len(req.Messages) {
		for _, message := range req.Messages[prepared.messageCount:] {
			historyChars += modelMessageChars(message)
		}
	}

	return modelRequestDiagnostics{
		systemPromptChars: prepared.systemPromptChars,
		historyChars:      historyChars,
		contextChars:      prepared.contextChars,
		userMessageChars:  prepared.userMessageChars,
		toolsChars:        toolDefinitionsChars(req.Tools),
		messageCount:      len(req.Messages),
		toolCount:         len(req.Tools),
	}
}

func recordModelRequestDiagnostics(
	ctx context.Context,
	route Route,
	diagnostics modelRequestDiagnostics,
) {
	attributes := []attribute.KeyValue{
		tracing.AssistantRequestSystemPromptChars.Int(diagnostics.systemPromptChars),
		tracing.AssistantRequestHistoryChars.Int(diagnostics.historyChars),
		tracing.AssistantRequestContextChars.Int(diagnostics.contextChars),
		tracing.AssistantRequestUserMessageChars.Int(diagnostics.userMessageChars),
		tracing.AssistantRequestToolsChars.Int(diagnostics.toolsChars),
		tracing.AssistantRequestMessageCount.Int(diagnostics.messageCount),
		tracing.AssistantRequestToolCount.Int(diagnostics.toolCount),
	}
	if route != "" {
		attributes = append(attributes, tracing.AssistantRoute.String(string(route)))
	}
	oteltrace.SpanFromContext(ctx).SetAttributes(attributes...)
}

func recordTerminalTool(ctx context.Context, toolName string) {
	oteltrace.SpanFromContext(ctx).SetAttributes(tracing.AssistantTerminalTool.String(toolName))
}

func textChars(value string) int {
	return utf8.RuneCountInString(value)
}

func modelMessageChars(message modelMessage) int {
	chars := utf8.RuneCountInString(message.Content) +
		utf8.RuneCountInString(message.Name) +
		utf8.RuneCountInString(message.ToolCallID)
	for _, toolCall := range message.ToolCalls {
		chars += utf8.RuneCountInString(toolCall.ID)
		chars += utf8.RuneCountInString(toolCall.Name)
		chars += utf8.RuneCountInString(toolCall.Arguments)
	}
	return chars
}

func toolDefinitionsChars(definitions []routing.ToolDefinition) int {
	total := 0
	for _, definition := range definitions {
		encoded, err := json.Marshal(struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		}{
			Name:        definition.Name,
			Description: definition.Description,
			Parameters:  definition.ParametersJSONSchema,
		})
		if err != nil {
			total += utf8.RuneCountInString(definition.Name)
			total += utf8.RuneCountInString(definition.Description)
			continue
		}
		total += utf8.RuneCount(encoded)
	}
	return total
}
