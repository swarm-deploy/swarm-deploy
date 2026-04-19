package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

const (
	defaultServiceLogsLimit = 200
	maxServiceLogsLimit     = 1000
)

// GetServiceLogs returns recent log lines from a stack service.
type GetServiceLogs struct {
	logsInspector ServiceLogsInspector
}

// NewGetServiceLogs creates service_logs_get component.
func NewGetServiceLogs(logsInspector ServiceLogsInspector) *GetServiceLogs {
	return &GetServiceLogs{
		logsInspector: logsInspector,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetServiceLogs) Definition() routing.ToolDefinition {
	limitDescription := fmt.Sprintf(
		"Page size from 1 to %d. Defaults to %d.",
		maxServiceLogsLimit,
		defaultServiceLogsLimit,
	)

	return routing.ToolDefinition{
		Name:        "service_logs_get",
		Description: "Returns recent logs from a specific stack service.",
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
				"limit": map[string]any{
					"type":        "integer",
					"description": limitDescription,
					"default":     defaultServiceLogsLimit,
					"minimum":     1,
					"maximum":     maxServiceLogsLimit,
				},
				"since": map[string]any{
					"type":        "string",
					"format":      "date-time",
					"description": "Optional lower time bound (RFC3339/RFC3339Nano).",
				},
				"until": map[string]any{
					"type":        "string",
					"format":      "date-time",
					"description": "Optional upper time bound (RFC3339/RFC3339Nano). Use next_until from previous page.",
				},
			},
		},
	}
}

// Execute runs service_logs_get tool.
func (g *GetServiceLogs) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	if g.logsInspector == nil {
		return routing.Response{}, fmt.Errorf("service logs inspector is not configured")
	}

	params, err := parseGetServiceLogsParams(request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	queryOptions := swarm.ServiceLogsOptions{
		Limit: params.Limit + 1,
		Since: params.Since,
		Until: params.Until,
	}
	if params.Until != nil {
		exclusiveUntil := params.Until.Add(-time.Nanosecond)
		queryOptions.Until = &exclusiveUntil
	}

	logs, err := g.logsInspector.Logs(ctx, params.StackName, params.ServiceName, queryOptions)
	if err != nil {
		return routing.Response{}, err
	}

	hasMore := len(logs) > params.Limit
	if hasMore {
		logs = logs[1:]
	}

	nextUntil := ""
	if hasMore && len(logs) > 0 {
		oldestLogTimestamp, parseErr := parseLogTimestamp(logs[0])
		if parseErr != nil {
			return routing.Response{}, fmt.Errorf("parse oldest log timestamp: %w", parseErr)
		}

		nextUntil = oldestLogTimestamp.Format(time.RFC3339Nano)
	}

	payload := struct {
		// StackName is a target stack name.
		StackName string `json:"stack_name"`

		// ServiceName is a target service name.
		ServiceName string `json:"service_name"`

		// Logs contains latest service log lines.
		Logs []string `json:"logs"`

		// Count is number of returned log lines.
		Count int `json:"count"`

		// AppliedSince is an applied lower time boundary.
		AppliedSince string `json:"applied_since,omitempty"`

		// AppliedUntil is an applied upper time boundary.
		AppliedUntil string `json:"applied_until,omitempty"`

		// HasMore reports whether there are older logs in selected window.
		HasMore bool `json:"has_more"`

		// NextUntil is a cursor for the next page of older logs.
		NextUntil string `json:"next_until,omitempty"`
	}{
		StackName:    params.StackName,
		ServiceName:  params.ServiceName,
		Logs:         logs,
		Count:        len(logs),
		AppliedSince: formatTimePointer(params.Since),
		AppliedUntil: formatTimePointer(params.Until),
		HasMore:      hasMore,
		NextUntil:    nextUntil,
	}

	return routing.Response{Payload: payload}, nil
}

type getServiceLogsParams struct {
	StackName   string
	ServiceName string
	Limit       int
	Since       *time.Time
	Until       *time.Time
}

func parseGetServiceLogsParams(payload map[string]any) (getServiceLogsParams, error) {
	stackName, err := parseStringParam(payload["stack_name"], "stack_name")
	if err != nil {
		return getServiceLogsParams{}, err
	}
	if stackName == "" {
		return getServiceLogsParams{}, fmt.Errorf("stack_name is required")
	}

	serviceName, err := parseStringParam(payload["service_name"], "service_name")
	if err != nil {
		return getServiceLogsParams{}, err
	}
	if serviceName == "" {
		return getServiceLogsParams{}, fmt.Errorf("service_name is required")
	}

	limit, err := parseServiceLogsLimit(payload["limit"])
	if err != nil {
		return getServiceLogsParams{}, err
	}

	sinceValue, hasSince, err := parseRFC3339TimestampParam(payload["since"], "since")
	if err != nil {
		return getServiceLogsParams{}, err
	}
	var since *time.Time
	if hasSince {
		since = &sinceValue
	}

	untilValue, hasUntil, err := parseRFC3339TimestampParam(payload["until"], "until")
	if err != nil {
		return getServiceLogsParams{}, err
	}
	var until *time.Time
	if hasUntil {
		until = &untilValue
	}

	if since != nil && until != nil && since.After(*until) {
		return getServiceLogsParams{}, fmt.Errorf("since must be before or equal to until")
	}

	return getServiceLogsParams{
		StackName:   stackName,
		ServiceName: serviceName,
		Limit:       limit,
		Since:       since,
		Until:       until,
	}, nil
}

func parseServiceLogsLimit(raw any) (int, error) {
	if raw == nil {
		return defaultServiceLogsLimit, nil
	}

	value, err := parseStringParam(raw, "limit")
	if err != nil {
		return 0, fmt.Errorf("limit must be integer")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("limit must be integer")
	}

	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("limit must be integer")
	}
	if limit <= 0 {
		return 0, fmt.Errorf("limit must be > 0")
	}
	if limit > maxServiceLogsLimit {
		return 0, fmt.Errorf("limit must be <= %d", maxServiceLogsLimit)
	}

	return limit, nil
}

func parseRFC3339TimestampParam(raw any, fieldName string) (time.Time, bool, error) {
	value, err := parseStringParam(raw, fieldName)
	if err != nil {
		return time.Time{}, false, err
	}
	if value == "" {
		return time.Time{}, false, nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s must be RFC3339/RFC3339Nano timestamp", fieldName)
	}

	return parsed.UTC(), true, nil
}

func parseLogTimestamp(logLine string) (time.Time, error) {
	trimmed := strings.TrimSpace(logLine)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("log line is empty")
	}

	timestampToken := trimmed
	if separatorIndex := strings.IndexByte(trimmed, ' '); separatorIndex > 0 {
		timestampToken = trimmed[:separatorIndex]
	}

	parsed, err := time.Parse(time.RFC3339Nano, timestampToken)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp %q", timestampToken)
	}

	return parsed.UTC(), nil
}

func formatTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}

	return value.UTC().Format(time.RFC3339Nano)
}
