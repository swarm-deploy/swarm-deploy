package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	mcpTools "github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/tools"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/metrics"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

// Executor provides direct-call MCP tools without running external server.
type Executor struct {
	tools       map[string]routing.Tool
	definitions []routing.ToolDefinition
	metrics     metrics.MCP
}

// NewExecutor creates an MCP tool executor from service components.
func NewExecutor(
	historyStore mcpTools.HistoryReader,
	nodesStore mcpTools.NodesReader,
	swarmService *swarm.Swarm,
	serviceStore mcpTools.ServicesReader,
	imageVersionResolver mcpTools.ImageVersionResolver,
	gitRepository mcpTools.GitRepository,
	stacks []config.StackSpec,
	commitDiffer mcpTools.CommitDiffer,
	control mcpTools.SyncTrigger,
	eventDispatcher dispatcher.Dispatcher,
	mcpMetrics metrics.MCP,
) *Executor {
	toolComponents := []routing.Tool{
		mcpTools.NewListHistoryEvents(historyStore),
		mcpTools.NewSync(control),
		mcpTools.NewListNodes(nodesStore),
		mcpTools.NewDockerNetworkList(swarmService.Networks),
		mcpTools.NewDockerPluginList(swarmService.Plugins),
		mcpTools.NewDockerSecretList(swarmService.Secrets),
		mcpTools.NewGetServiceLogs(swarmService.Services),
		mcpTools.NewGetServiceSpec(swarmService.Services),
		mcpTools.NewDNSNameResolve(),
		mcpTools.NewPingWebRoutes(serviceStore),
		mcpTools.NewSetServiceReplicas(swarmService.Services, eventDispatcher),
		mcpTools.NewRestartService(swarmService.Services, eventDispatcher),
		mcpTools.NewGetActualImageVersion(imageVersionResolver),
		mcpTools.NewListGitCommits(gitRepository),
		mcpTools.NewGitCommitDiff(gitRepository, stacks, commitDiffer),
		mcpTools.NewDate(),
		mcpTools.NewReportPromptInjection(eventDispatcher),
	}

	tools := make(map[string]routing.Tool, len(toolComponents))
	definitions := make([]routing.ToolDefinition, 0, len(toolComponents))

	for _, tool := range toolComponents {
		definition := tool.Definition()
		tools[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	return &Executor{
		tools:       tools,
		definitions: definitions,
		metrics:     mcpMetrics,
	}
}

// Definitions returns available MCP tool metadata.
func (e *Executor) Definitions() []routing.ToolDefinition {
	return e.definitions
}

// Execute runs a tool by name.
func (e *Executor) Execute(ctx context.Context, req routing.Request) (string, error) {
	startedAt := time.Now()
	success := false
	defer func() {
		e.metrics.RecordToolExecution(req.ToolName, success, time.Since(startedAt))
	}()

	tool, ok := e.tools[req.ToolName]
	if !ok {
		e.metrics.RecordUnknownTool(req.ToolName)

		return "", fmt.Errorf("unknown tool %q", req.ToolName)
	}

	result, err := tool.Execute(ctx, req)
	if err != nil {
		return "", err
	}

	encoded, err := json.Marshal(result.Payload)
	if err != nil {
		return "", fmt.Errorf("encode %q tool response: %w", req.ToolName, err)
	}

	success = true
	return string(encoded), nil
}
