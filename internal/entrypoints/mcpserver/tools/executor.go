package tools

import (
	"context"
	"fmt"

	"github.com/artarts36/swarm-deploy/internal/assistant"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
)

// Executor provides direct-call MCP tools without running external server.
type Executor struct {
	tools       map[string]Tool
	definitions []assistant.ToolDefinition
}

var _ assistant.ToolExecutor = (*Executor)(nil)

// NewExecutor creates an MCP tool executor from independent tool components.
func NewExecutor(
	historyStore HistoryReader,
	nodesStore NodesReader,
	control SyncTrigger,
	eventDispatcher dispatcher.Dispatcher,
) *Executor {
	toolComponents := []Tool{
		NewListHistoryEvents(historyStore),
		NewSync(control),
		NewListNodes(nodesStore),
		NewReportPromptInjection(eventDispatcher),
	}

	tools := make(map[string]Tool, len(toolComponents))
	definitions := make([]assistant.ToolDefinition, 0, len(toolComponents))

	for _, tool := range toolComponents {
		definition := tool.Definition()
		tools[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	return &Executor{
		tools:       tools,
		definitions: definitions,
	}
}

// Definitions returns available MCP tool metadata.
func (e *Executor) Definitions() []assistant.ToolDefinition {
	definitions := make([]assistant.ToolDefinition, len(e.definitions))
	copy(definitions, e.definitions)

	return definitions
}

// Execute runs a tool by name.
func (e *Executor) Execute(_ context.Context, name string, arguments map[string]any) (string, error) {
	tool, ok := e.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}

	return tool.Execute(arguments)
}
