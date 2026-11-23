package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	defaultServiceTaskListLimit = 20
	maxServiceTaskListLimit     = 100
)

// ListServiceTasks returns Docker Swarm tasks for a stack service.
type ListServiceTasks struct {
	services swarm.ServiceManager
}

type listServiceTasksRequest struct {
	StackName   string `json:"stack_name"`
	ServiceName string `json:"service_name"`
	Limit       *int   `json:"limit"`
	Offset      int    `json:"offset"`
}

type serviceTaskResponse struct {
	// ID is the Docker task identifier accepted by service_task_logs_get.
	ID string `json:"id"`
	// Node is the Docker node identifier assigned to the task.
	Node string `json:"node"`
	// State is the current Docker Swarm task state.
	State swarm.TaskState `json:"state"`
	// Error is the task runtime error reported by Docker Swarm.
	Error string `json:"error"`
	// CreatedAt is the task creation timestamp.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the task last update timestamp.
	UpdatedAt time.Time `json:"updated_at"`
}

// NewListServiceTasks creates the service_task_list component.
func NewListServiceTasks(services swarm.ServiceManager) *ListServiceTasks {
	return &ListServiceTasks{services: services}
}

// Definition returns tool metadata visible to the model.
func (l *ListServiceTasks) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: config.AssistantToolNameServiceTaskList,
		Description: "Returns Docker Swarm tasks for a stack service, including historical and terminal tasks, " +
			"newest first. Task IDs can be passed to service_task_logs_get.",
		ParametersJSONSchema: map[string]any{
			"type":     "object",
			"required": []string{"stack_name", "service_name"},
			"properties": map[string]any{
				"stack_name": map[string]any{
					"type":        "string",
					"description": "Docker Swarm stack name.",
				},
				"service_name": map[string]any{
					"type":        "string",
					"description": "Service name inside the stack.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Page size from 1 to %d.", maxServiceTaskListLimit),
					"default":     defaultServiceTaskListLimit,
					"minimum":     1,
					"maximum":     maxServiceTaskListLimit,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Zero-based offset. Use next_offset to access older tasks.",
					"default":     0,
					"minimum":     0,
				},
			},
		},
		Request: listServiceTasksRequest{},
	}
}

// Execute runs the service_task_list tool.
func (l *ListServiceTasks) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[listServiceTasksRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	params, err := parseListServiceTasksParams(parsedRequest)
	if err != nil {
		return routing.Response{}, err
	}

	tasks, err := l.services.ListTasks(
		ctx,
		swarm.NewServiceReference(params.StackName, params.ServiceName).Name(),
	)
	if err != nil {
		return routing.Response{}, err
	}

	sortedTasks := append([]swarm.ServiceTask(nil), tasks...)
	sort.Slice(sortedTasks, func(i, j int) bool {
		if !sortedTasks[i].CreatedAt.Equal(sortedTasks[j].CreatedAt) {
			return sortedTasks[i].CreatedAt.After(sortedTasks[j].CreatedAt)
		}
		if !sortedTasks[i].UpdatedAt.Equal(sortedTasks[j].UpdatedAt) {
			return sortedTasks[i].UpdatedAt.After(sortedTasks[j].UpdatedAt)
		}
		return sortedTasks[i].ID > sortedTasks[j].ID
	})

	start := min(params.Offset, len(sortedTasks))
	end := min(start+params.Limit, len(sortedTasks))
	page := sortedTasks[start:end]

	items := make([]serviceTaskResponse, 0, len(page))
	for _, task := range page {
		items = append(items, serviceTaskResponse{
			ID:        task.ID,
			Node:      task.Node,
			State:     task.CurrentState,
			Error:     task.Error,
			CreatedAt: task.CreatedAt,
			UpdatedAt: task.UpdatedAt,
		})
	}

	hasMore := end < len(sortedTasks)
	var nextOffset *int
	if hasMore {
		value := end
		nextOffset = &value
	}

	payload := struct {
		// StackName is the target stack name.
		StackName string `json:"stack_name"`
		// ServiceName is the target service name.
		ServiceName string `json:"service_name"`
		// Tasks contains the requested page, including terminal and historical tasks.
		Tasks []serviceTaskResponse `json:"tasks"`
		// Count is the number of tasks in this page.
		Count int `json:"count"`
		// Offset is the applied zero-based offset.
		Offset int `json:"offset"`
		// HasMore reports whether older tasks are available.
		HasMore bool `json:"has_more"`
		// NextOffset is the offset to use for the next page of older tasks.
		NextOffset *int `json:"next_offset,omitempty"`
	}{
		StackName:   params.StackName,
		ServiceName: params.ServiceName,
		Tasks:       items,
		Count:       len(items),
		Offset:      params.Offset,
		HasMore:     hasMore,
		NextOffset:  nextOffset,
	}

	return routing.Response{Payload: payload}, nil
}

type listServiceTasksParams struct {
	StackName   string
	ServiceName string
	Limit       int
	Offset      int
}

func parseListServiceTasksParams(request listServiceTasksRequest) (listServiceTasksParams, error) {
	stackName := strings.TrimSpace(request.StackName)
	if stackName == "" {
		return listServiceTasksParams{}, fmt.Errorf("stack_name is required")
	}

	serviceName := strings.TrimSpace(request.ServiceName)
	if serviceName == "" {
		return listServiceTasksParams{}, fmt.Errorf("service_name is required")
	}

	limit := defaultServiceTaskListLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	if limit <= 0 {
		return listServiceTasksParams{}, fmt.Errorf("limit must be > 0")
	}
	if limit > maxServiceTaskListLimit {
		return listServiceTasksParams{}, fmt.Errorf("limit must be <= %d", maxServiceTaskListLimit)
	}
	if request.Offset < 0 {
		return listServiceTasksParams{}, fmt.Errorf("offset must be >= 0")
	}

	return listServiceTasksParams{
		StackName:   stackName,
		ServiceName: serviceName,
		Limit:       limit,
		Offset:      request.Offset,
	}, nil
}
