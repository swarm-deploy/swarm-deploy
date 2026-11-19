package tools

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

const selfMetricsNamePrefix = "swarm_deploy_"

// Executor provides built-in assistant tools.
type Executor struct {
	tools               map[string]routing.Tool
	definitions         []routing.ToolDefinition
	requests            map[string]any
	metrics             metrics.MCP
	tracer              trace.Tracer
	syncPauses          *syncpause.Service
	configToolAllowlist map[string]struct{}
}

// NewExecutor creates an assistant tool executor from service components.
func NewExecutor(
	resourcesModule *resources.Module,
	gitopsModule *gitops.Module,
	deployments *deploymentstore.Store,
	alertModule *alertmanagement.Module,
	eventModule *event.Module,
	swarmService *swarm.Swarm,
	taskLogs *logparser.Service,
	recommendations RecommendationsReader,
	imageVersionResolver ImageVersionResolver,
	hostingProviders *githosting.ProviderManager,
	stacks []config.StackSpec,
	mcpMetrics metrics.MCP,
	spec config.Spec,
) *Executor {
	toolComponents := []routing.Tool{
		NewListHistoryEvents(eventModule.History),
		NewAlertList(alertModule.Store),
		NewAlertGet(alertModule.Store),
		NewDeploymentList(deployments),
		NewDeploymentGet(deployments),
		NewSyncPauseGet(gitopsModule.SyncPauses),
		NewSyncPauseSet(gitopsModule.SyncPauses),
		NewSyncPauseResume(gitopsModule.SyncPauses),
		NewSync(gitopsModule.Controller),
		NewListNodes(resourcesModule.NodeStore),
		NewDockerNetworkList(swarmService.Networks),
		NewDockerPluginList(swarmService.Plugins),
		NewDockerSecretList(swarmService.Secrets),
		NewGetServiceLogs(swarmService.Services),
		NewListServiceTasks(swarmService.Services),
		NewGetTaskLogs(taskLogs),
		NewGetServiceSpec(swarmService.Services),
		NewServiceHealthGet(
			swarmService.Services, resourcesModule.NodeStore, alertModule.Store, deployments,
		),
		NewDNSNameResolve(),
		NewTLSCertificateCheck(),
		NewPingWebRoutes(resourcesModule.ServiceStore),
		NewGetDependencyGraph(resourcesModule.ServiceStore),
		NewRecommendationList(recommendations),
		NewSetServiceReplicas(swarmService.Services, eventModule.Dispatcher),
		NewRestartService(swarmService.Services, eventModule.Dispatcher),
		NewGetActualImageVersion(imageVersionResolver),
		NewGitRepositoryDiagnostics(gitopsModule.GitRepository, gitopsModule.Store, alertModule.Store,
			spec.GitOps.Git.Repository),
		NewListGitCommits(gitopsModule.GitRepository),
		NewGitCommitDiff(gitopsModule.GitRepository, stacks, gitopsModule.Differ),
		NewGetExternalRepositoryLatestRelease(hostingProviders),
		NewDate(),
		NewSelfMetricsList(prometheus.DefaultGatherer, selfMetricsNamePrefix),
		NewReportPromptInjection(eventModule.Dispatcher),
	}

	allowed := config.NormalizeAssistantToolAllowlist(spec.Assistant.Tools)
	toolComponents = append(toolComponents, newSwarmDeployConfigGet(spec, allowed, toolComponents))

	tools := make(map[string]routing.Tool, len(toolComponents))
	requests := make(map[string]any, len(toolComponents))
	definitions := make([]routing.ToolDefinition, 0, len(toolComponents))

	for _, tool := range toolComponents {
		definition := tool.Definition()
		toolName := string(definition.Name)
		tools[toolName] = tool
		requests[toolName] = definition.Request
		definitions = append(definitions, definition)
	}

	return &Executor{
		tools:               tools,
		definitions:         definitions,
		requests:            requests,
		metrics:             mcpMetrics,
		tracer:              otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools"),
		syncPauses:          gitopsModule.SyncPauses,
		configToolAllowlist: allowed,
	}
}

// StackSyncPause returns GitOps-owned stack pause state for confirmation warnings.
func (e *Executor) StackSyncPause(ctx context.Context, stack string) (syncpause.StackState, error) {
	return e.syncPauses.GetStack(ctx, stack)
}

// ServiceSyncPause revalidates a GitOps-owned service target.
func (e *Executor) ServiceSyncPause(ctx context.Context, stack, service string) (syncpause.ServiceState, error) {
	return e.syncPauses.GetService(ctx, stack, service)
}

// Definitions returns available assistant tool metadata.
func (e *Executor) Definitions() []routing.ToolDefinition {
	return e.definitions
}

// Execute runs a tool by name.
func (e *Executor) Execute(ctx context.Context, req routing.Request) (string, error) {
	if req.ToolName == configGetToolName {
		return e.executeConfigGet(ctx, req)
	}

	raw, _ := req.Payload.(string)
	ctx, span := e.tracer.Start(ctx, "tool "+req.ToolName, trace.WithAttributes(
		semconv.GenAIOperationNameExecuteTool,
		tracing.GenAIToolName.String(req.ToolName),
		tracing.GenAIToolType.String("function"),
		tracing.GenAIToolCallArguments.String(raw),
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

	slog.InfoContext(ctx, "[assistant-tool] executing tool",
		slog.String("tool.name", req.ToolName),
		slog.Any("request", req.Payload),
	)

	result, err := tool.Execute(ctx, req)
	if err != nil {
		tracing.FailSpan(span, fmt.Errorf("failed to execute tool: %w", err))

		return "", err
	}

	encoded, err := json.Marshal(result.Payload, jsonv1.DefaultOptionsV1())
	if err != nil {
		tracing.FailSpan(span, fmt.Errorf("failed to encode payload: %w", err))

		return "", fmt.Errorf("encode %q tool response: %w", req.ToolName, err)
	}

	if toolResultCanBeTraced(req.ToolName) {
		span.SetAttributes(tracing.GenAIToolCallResult.String(string(encoded)))
	} else {
		span.SetAttributes(tracing.AssistantToolResultChars.Int(len(encoded)))
	}
	span.SetStatus(codes.Ok, "success")

	success = true
	return string(encoded), nil
}

// The global configuration tool validates its own raw arguments and records only execution metadata.
func (e *Executor) executeConfigGet(ctx context.Context, req routing.Request) (encoded string, resultErr error) {
	ctx, span := e.tracer.Start(ctx, "tool "+configGetToolName, trace.WithAttributes(
		semconv.GenAIOperationNameExecuteTool,
		tracing.GenAIToolName.String(configGetToolName),
	))
	startedAt := time.Now()
	defer func() {
		duration := time.Since(startedAt)
		success := resultErr == nil
		e.metrics.RecordToolExecution(configGetToolName, success, duration)
		if success {
			span.SetStatus(codes.Ok, "")
		} else {
			span.SetStatus(codes.Error, "")
		}
		span.SetAttributes(tracing.AssistantToolResultChars.Int(utf8.RuneCountInString(encoded)))
		span.End()

		slog.InfoContext(ctx, "[assistant-tool] configuration lookup finished",
			slog.String("tool.name", configGetToolName),
			slog.Bool("success", success),
			slog.Duration("duration", duration),
			slog.Int("response_size", len(encoded)),
		)
	}()

	if len(e.configToolAllowlist) > 0 {
		if _, allowed := e.configToolAllowlist[configGetToolName]; !allowed {
			return "", errors.New("tool is not allowed by assistant.tools")
		}
	}

	result, err := e.tools[configGetToolName].Execute(ctx, req)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(result.Payload, jsonv1.DefaultOptionsV1())
	if err != nil {
		return "", errors.New("could not encode configuration response")
	}
	return string(payload), nil
}

func toolResultCanBeTraced(toolName string) bool {
	return toolName != string(config.AssistantToolNameServiceLogsGet) &&
		toolName != string(config.AssistantToolNameServiceTaskLogsGet)
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
