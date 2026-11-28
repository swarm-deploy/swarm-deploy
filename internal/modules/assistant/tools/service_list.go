package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

const (
	defaultServiceListLimit = 50
	maxServiceListLimit     = 100
)

// ServiceList returns compact entries from the persisted service catalog.
type ServiceList struct {
	services ServicesReader
}

type serviceListRequest struct {
	// Stack optionally restricts results to one Docker stack.
	Stack string `json:"stack"`
	// Limit is the maximum page size.
	Limit *int `json:"limit"`
	// Offset is the zero-based page offset.
	Offset int `json:"offset"`
}

type serviceListItem struct {
	// Stack is the Docker stack containing the service.
	Stack string `json:"stack"`
	// Name is the service name inside the stack.
	Name string `json:"name"`
	// Type is the normalized service classification.
	Type string `json:"type"`
	// Description is the human-readable service description.
	Description string `json:"description"`
}

// NewServiceList creates the service_list component.
func NewServiceList(services ServicesReader) *ServiceList {
	return &ServiceList{services: services}
}

// Definition returns tool metadata visible to the model.
func (l *ServiceList) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: config.AssistantToolNameServiceList,
		Description: "Returns compact persisted service catalog entries. Use when the user asks to list services " +
			"or when a target is absent from retrieved service metadata.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"stack": map[string]any{
					"type":        "string",
					"description": "Optional Docker stack name to filter services.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Page size from 1 to %d.", maxServiceListLimit),
					"default":     defaultServiceListLimit,
					"minimum":     1,
					"maximum":     maxServiceListLimit,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Zero-based offset. Use next_offset to access more services.",
					"default":     0,
					"minimum":     0,
				},
			},
		},
		Request: serviceListRequest{},
	}
}

// Execute runs the service_list tool.
func (l *ServiceList) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[serviceListRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	params, err := parseServiceListParams(parsedRequest)
	if err != nil {
		return routing.Response{}, err
	}

	services, err := l.services.ReadAll(ctx)
	if err != nil {
		return routing.Response{}, fmt.Errorf("list service catalog: %w", err)
	}

	items := make([]serviceListItem, 0, len(services))
	for _, service := range services {
		if params.Stack != "" && service.Stack != params.Stack {
			continue
		}
		items = append(items, serviceListItem{
			Stack:       service.Stack,
			Name:        service.Name,
			Type:        string(service.Type),
			Description: service.Description,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Stack != items[j].Stack {
			return items[i].Stack < items[j].Stack
		}
		return items[i].Name < items[j].Name
	})

	start := min(params.Offset, len(items))
	end := min(start+params.Limit, len(items))
	page := items[start:end]
	hasMore := end < len(items)
	var nextOffset *int
	if hasMore {
		value := end
		nextOffset = &value
	}

	payload := struct {
		// Services contains compact service catalog entries.
		Services []serviceListItem `json:"services"`
		// Count is the number of services in this page.
		Count int `json:"count"`
		// Offset is the applied zero-based offset.
		Offset int `json:"offset"`
		// HasMore reports whether more services are available.
		HasMore bool `json:"has_more"`
		// NextOffset is the offset to use for the next page.
		NextOffset *int `json:"next_offset,omitempty"`
	}{
		Services:   page,
		Count:      len(page),
		Offset:     params.Offset,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}

	return routing.Response{Payload: payload}, nil
}

type serviceListParams struct {
	// Stack optionally restricts results to one Docker stack.
	Stack string
	// Limit is the validated page size.
	Limit int
	// Offset is the validated zero-based page offset.
	Offset int
}

func parseServiceListParams(request serviceListRequest) (serviceListParams, error) {
	limit := defaultServiceListLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	if limit <= 0 {
		return serviceListParams{}, fmt.Errorf("limit must be > 0")
	}
	if limit > maxServiceListLimit {
		return serviceListParams{}, fmt.Errorf("limit must be <= %d", maxServiceListLimit)
	}
	if request.Offset < 0 {
		return serviceListParams{}, fmt.Errorf("offset must be >= 0")
	}

	return serviceListParams{
		Stack:  strings.TrimSpace(request.Stack),
		Limit:  limit,
		Offset: request.Offset,
	}, nil
}
