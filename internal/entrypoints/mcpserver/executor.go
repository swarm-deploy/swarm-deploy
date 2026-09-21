package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	mcpTools "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/tools"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const selfMetricsNamePrefix = "swarm_deploy_"

// Executor provides direct-call MCP tools without running external server.
type Executor struct {
	tools       map[string]routing.Tool
	definitions []routing.ToolDefinition
	requests    map[string]any
	metrics     metrics.MCP
	tracer      trace.Tracer
}

// NewExecutor creates an MCP tool executor from service components.
func NewExecutor(
	resourcesModule *resources.Module,
	gitopsModule *gitops.Module,
	eventModule *event.Module,
	swarmService *swarm.Swarm,
	recommendations mcpTools.RecommendationsReader,
	imageVersionResolver mcpTools.ImageVersionResolver,
	hostingProviders *githosting.ProviderManager,
	stacks []config.StackSpec,
	mcpMetrics metrics.MCP,
) *Executor {
	toolComponents := []routing.Tool{
		mcpTools.NewListHistoryEvents(eventModule.History),
		mcpTools.NewSync(gitopsModule.Controller),
		mcpTools.NewListNodes(resourcesModule.NodeStore),
		mcpTools.NewDockerNetworkList(swarmService.Networks),
		mcpTools.NewDockerPluginList(swarmService.Plugins),
		mcpTools.NewDockerSecretList(swarmService.Secrets),
		mcpTools.NewGetServiceLogs(swarmService.Services),
		mcpTools.NewGetServiceSpec(swarmService.Services),
		mcpTools.NewDNSNameResolve(),
		mcpTools.NewPingWebRoutes(resourcesModule.ServiceStore),
		mcpTools.NewGetDependencyGraph(resourcesModule.ServiceStore),
		mcpTools.NewRecommendationList(recommendations),
		mcpTools.NewSetServiceReplicas(swarmService.Services, eventModule.Dispatcher),
		mcpTools.NewRestartService(swarmService.Services, eventModule.Dispatcher),
		mcpTools.NewGetActualImageVersion(imageVersionResolver),
		mcpTools.NewListGitCommits(gitopsModule.GitRepository),
		mcpTools.NewGitCommitDiff(gitopsModule.GitRepository, stacks, gitopsModule.Differ),
		mcpTools.NewGetExternalRepositoryLatestRelease(hostingProviders),
		mcpTools.NewDate(),
		mcpTools.NewSelfMetricsList(prometheus.DefaultGatherer, selfMetricsNamePrefix),
		mcpTools.NewReportPromptInjection(eventModule.Dispatcher),
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
		tracer:      otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver"),
	}
}

// Definitions returns available MCP tool metadata.
func (e *Executor) Definitions() []routing.ToolDefinition {
	return e.definitions
}

// Execute runs a tool by name.
func (e *Executor) Execute(ctx context.Context, req routing.Request) (string, error) {
	ctx, span := e.tracer.Start(ctx, "MCP "+req.ToolName, trace.WithAttributes(
		tracing.GenAIToolName.String(req.ToolName),
		tracing.GenAIToolType.String("function"),
		tracing.GenAIToolCallArguments.String(req.Payload.(string)),
	))
	defer span.End()

	startedAt := time.Now()
	success := false
	defer func() {
		e.metrics.RecordToolExecution(req.ToolName, success, time.Since(startedAt))
	}()

	tool, ok := e.tools[req.ToolName]
	if !ok {
		tracing.FailSpan(span, errors.New("tool not found"))
		e.metrics.RecordUnknownTool(req.ToolName)

		return "", fmt.Errorf("unknown tool %q", req.ToolName)
	}

	span.SetAttributes(tracing.GenAIToolDescription.String(tool.Definition().Description))

	decodedPayload, err := decodeToolRequestPayload(req.Payload, e.requests[req.ToolName])
	if err != nil {
		tracing.FailSpan(span, fmt.Errorf("failed to decode payload: %w", err))

		return "", fmt.Errorf("decode %q request payload: %w", req.ToolName, err)
	}
	req.Payload = decodedPayload

	slog.InfoContext(ctx, "[mcp-executor] executing tool",
		slog.String("tool.name", req.ToolName),
		slog.Any("request", req.Payload),
	)

	result, err := tool.Execute(ctx, req)
	if err != nil {
		tracing.FailSpan(span, fmt.Errorf("failed to execute tool: %w", err))

		return "", err
	}

	encoded, err := json.Marshal(result.Payload)
	if err != nil {
		tracing.FailSpan(span, fmt.Errorf("failed to encode payload: %w", err))

		return "", fmt.Errorf("encode %q tool response: %w", req.ToolName, err)
	}

	span.SetAttributes(tracing.GenAIToolCallResult.String(string(encoded)))
	span.SetStatus(codes.Ok, "success")

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
