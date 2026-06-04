package tools

import (
	"context"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// GetServiceHealth returns runtime health summary for a stack service.
type GetServiceHealth struct {
	inspector swarm.ServiceManager
}

type getServiceHealthRequest struct {
	StackName   string `json:"stack_name"`
	ServiceName string `json:"service_name"`
}

// NewGetServiceHealth creates service_health_get component.
func NewGetServiceHealth(serviceInspector swarm.ServiceManager) *GetServiceHealth {
	return &GetServiceHealth{
		inspector: serviceInspector,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetServiceHealth) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "service_health_get",
		Description: "Returns runtime health summary and task states for a stack service.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"stack_name",
				"service_name",
			},
			"properties": map[string]any{
				"stack_name": map[string]any{
					"type":        "string",
					"description": "Docker Swarm stack name.",
				},
				"service_name": map[string]any{
					"type":        "string",
					"description": "Service name inside the stack.",
				},
			},
		},
		Request: getServiceHealthRequest{},
	}
}

// Execute runs service_health_get tool.
func (g *GetServiceHealth) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[getServiceHealthRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	stackName, err := parseRequiredStringParam(parsedRequest.StackName, "stack_name")
	if err != nil {
		return routing.Response{}, err
	}

	serviceName, err := parseRequiredStringParam(parsedRequest.ServiceName, "service_name")
	if err != nil {
		return routing.Response{}, err
	}

	serviceRef := swarm.NewServiceReference(stackName, serviceName)

	service, err := g.inspector.Get(ctx, serviceRef)
	if err != nil {
		return routing.Response{}, err
	}

	tasks, err := g.inspector.ListTasks(ctx, serviceRef)
	if err != nil {
		return routing.Response{}, err
	}

	summary := summarizeServiceHealth(service, tasks)

	payload := struct {
		// StackName is a target stack name.
		StackName string `json:"stack_name"`
		// ServiceName is a target service name.
		ServiceName string `json:"service_name"`
		// Mode is a current Docker Swarm service mode.
		Mode string `json:"mode"`
		// DesiredReplicas is a desired replicas count for replicated services.
		DesiredReplicas *uint64 `json:"desired_replicas,omitempty"`
		// HealthStatus is a synthesized service health summary.
		HealthStatus string `json:"health_status"`
		// RunningTasks is a count of tasks in running state.
		RunningTasks int `json:"running_tasks"`
		// ProgressingTasks is a count of non-terminal tasks still starting up.
		ProgressingTasks int `json:"progressing_tasks"`
		// TerminalErrorTasks is a count of observed terminal error states.
		TerminalErrorTasks int `json:"terminal_error_tasks"`
		// TotalTasks is a total number of returned tasks.
		TotalTasks int `json:"total_tasks"`
		// StateCounts contains counts by raw Docker task state.
		StateCounts map[string]int `json:"state_counts"`
		// UpdateStatus contains current rolling update status when available.
		UpdateStatus *swarm.ServiceUpdateStatus `json:"update_status,omitempty"`
		// Tasks contains raw service task snapshots.
		Tasks []swarm.ServiceTask `json:"tasks"`
	}{
		StackName:          stackName,
		ServiceName:        serviceName,
		Mode:               service.Spec.Mode,
		DesiredReplicas:    desiredReplicasPointer(service.Spec),
		HealthStatus:       summary.Status,
		RunningTasks:       summary.RunningTasks,
		ProgressingTasks:   summary.ProgressingTasks,
		TerminalErrorTasks: summary.TerminalErrorTasks,
		TotalTasks:         len(tasks),
		StateCounts:        summary.StateCounts,
		UpdateStatus:       service.UpdateStatus,
		Tasks:              tasks,
	}

	return routing.Response{Payload: payload}, nil
}

type serviceHealthSummary struct {
	Status             string
	RunningTasks       int
	ProgressingTasks   int
	TerminalErrorTasks int
	StateCounts        map[string]int
}

func summarizeServiceHealth(service swarm.Service, tasks []swarm.ServiceTask) serviceHealthSummary {
	summary := serviceHealthSummary{
		StateCounts: make(map[string]int),
	}

	for _, task := range tasks {
		state := normalizeServiceTaskState(task.CurrentState)
		summary.StateCounts[state]++

		switch {
		case state == "running":
			summary.RunningTasks++
		case isProgressingServiceTaskState(state):
			summary.ProgressingTasks++
		case isTerminalErrorServiceTaskState(state):
			summary.TerminalErrorTasks++
		}
	}

	summary.Status = resolveServiceHealthStatus(service, summary)
	return summary
}

func normalizeServiceTaskState(state string) string {
	state = strings.TrimSpace(state)
	if state == "" {
		return "unknown"
	}

	return state
}

func resolveServiceHealthStatus(service swarm.Service, summary serviceHealthSummary) string {
	if service.Spec.Mode == "replicated" && service.Spec.Replicas == 0 {
		return "scaled_to_zero"
	}

	if hasActiveServiceUpdate(service.UpdateStatus) {
		return "updating"
	}

	if service.Spec.Mode == "replicated" {
		if summary.RunningTasks >= int(service.Spec.Replicas) && summary.ProgressingTasks == 0 {
			return "healthy"
		}
	} else if summary.RunningTasks > 0 && summary.ProgressingTasks == 0 {
		return "healthy"
	}

	if summary.RunningTasks > 0 || summary.ProgressingTasks > 0 {
		return "degraded"
	}

	if summary.TerminalErrorTasks > 0 {
		return "failed"
	}

	return "unknown"
}

func hasActiveServiceUpdate(status *swarm.ServiceUpdateStatus) bool {
	if status == nil {
		return false
	}

	switch strings.TrimSpace(status.State) {
	case "", "completed", "rollback_completed":
		return false
	default:
		return true
	}
}

func isProgressingServiceTaskState(state string) bool {
	switch state {
	case "new", "allocated", "pending", "assigned", "accepted", "preparing", "ready", "starting":
		return true
	default:
		return false
	}
}

func isTerminalErrorServiceTaskState(state string) bool {
	switch state {
	case "failed", "rejected", "orphaned":
		return true
	default:
		return false
	}
}

func desiredReplicasPointer(spec swarm.ServiceSpec) *uint64 {
	if spec.Mode != "replicated" {
		return nil
	}

	replicas := spec.Replicas
	return &replicas
}
