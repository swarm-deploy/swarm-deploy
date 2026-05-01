package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	mcpTools "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const selfMetricsNamePrefix = "swarm_deploy_"

// Executor provides direct-call MCP tools without running external server.
type Executor struct {
	tools       map[string]routing.Tool
	definitions []routing.ToolDefinition
	requests    map[string]any
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
		mcpTools.NewSelfMetricsList(prometheus.DefaultGatherer, selfMetricsNamePrefix),
		mcpTools.NewReportPromptInjection(eventDispatcher),
	}

	tools := make(map[string]routing.Tool, len(toolComponents))
	requests := make(map[string]any, len(toolComponents))
	definitions := make([]routing.ToolDefinition, 0, len(toolComponents))

	for _, tool := range toolComponents {
		definition := tool.Definition()
		tools[definition.Name] = tool
		requests[definition.Name] = definition.Request
		definitions = append(definitions, definition)
	}

	return &Executor{
		tools:       tools,
		definitions: definitions,
		requests:    requests,
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

	decodedPayload, err := decodeToolRequestPayload(req.Payload, e.requests[req.ToolName])
	if err != nil {
		return "", fmt.Errorf("decode %q request payload: %w", req.ToolName, err)
	}
	req.Payload = decodedPayload

	slog.InfoContext(ctx, "[mcp-executor] executing tool",
		slog.String("tool.name", req.ToolName),
		slog.Any("request", req.Payload),
	)

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

func decodeToolRequestPayload(payload any, requestShape any) (any, error) {
	if requestShape == nil {
		return payload, nil
	}

	requestType := reflect.TypeOf(requestShape)
	if requestType == nil {
		return payload, nil
	}

	if payload == nil {
		return reflect.Zero(requestType).Interface(), nil
	}

	if reflect.TypeOf(payload) == requestType {
		return payload, nil
	}

	decoded := reflect.New(requestType)

	switch raw := payload.(type) {
	case string:
		if strings.TrimSpace(raw) == "" {
			return reflect.Zero(requestType).Interface(), nil
		}

		if err := json.Unmarshal([]byte(raw), decoded.Interface()); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
	case []byte:
		if len(raw) == 0 {
			return reflect.Zero(requestType).Interface(), nil
		}

		if err := json.Unmarshal(raw, decoded.Interface()); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
	default:
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode payload: %w", err)
		}

		if err = json.Unmarshal(encoded, decoded.Interface()); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
	}

	return decoded.Elem().Interface(), nil
}
