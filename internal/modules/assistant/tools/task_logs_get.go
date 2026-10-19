package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/tasklogs"
)

const (
	defaultTaskLogsLimit    = 100
	maxTaskLogsLimit        = 500
	maxTaskLogFieldBytes    = 4096
	maxTaskLogsPayloadBytes = 64 * 1024
	toolSchemaDefaultKey    = "default"
	toolSchemaArrayType     = "array"
	toolSchemaDateTime      = "date-time"
)

// GetTaskLogs returns bounded raw or structured logs for a Docker Swarm task.
type GetTaskLogs struct {
	logs *tasklogs.Service
}

type getTaskLogsRequest struct {
	TaskID string   `json:"task_id"`
	Parse  bool     `json:"parse"`
	Levels []string `json:"levels"`
	Limit  *int     `json:"limit"`
	Since  string   `json:"since"`
	Until  string   `json:"until"`
}

// NewGetTaskLogs creates the service_task_logs_get component.
func NewGetTaskLogs(logs *tasklogs.Service) *GetTaskLogs {
	return &GetTaskLogs{logs: logs}
}

// Definition returns tool metadata visible to the model.
func (g *GetTaskLogs) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: config.AssistantToolNameServiceTaskLogsGet,
		Description: "Returns bounded logs for a specific Docker Swarm task with optional shared " +
			"JSON/logfmt parsing and normalized level filtering.",
		ParametersJSONSchema: map[string]any{
			"type":     "object",
			"required": []string{"task_id"},
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Docker Swarm task ID.",
				},
				"parse": map[string]any{
					"type":               "boolean",
					"description":        "Parse JSON/logfmt logs into timestamp, level, message, attributes, and raw_message.",
					toolSchemaDefaultKey: false,
				},
				"levels": map[string]any{
					"type":        toolSchemaArrayType,
					"description": "Optional normalized level filters. Requires parse=true.",
					"items": map[string]any{
						"type": "string",
						"enum": []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"},
					},
				},
				"limit": map[string]any{
					"type":               "integer",
					"description":        fmt.Sprintf("Maximum inspected and returned lines from 1 to %d.", maxTaskLogsLimit),
					toolSchemaDefaultKey: defaultTaskLogsLimit,
					"minimum":            1,
					"maximum":            maxTaskLogsLimit,
				},
				"since": map[string]any{
					"type":        "string",
					"format":      toolSchemaDateTime,
					"description": "Optional inclusive lower time bound (RFC3339/RFC3339Nano).",
				},
				"until": map[string]any{
					"type":        "string",
					"format":      toolSchemaDateTime,
					"description": "Optional inclusive upper time bound (RFC3339/RFC3339Nano).",
				},
			},
		},
		Request: getTaskLogsRequest{},
	}
}

// Execute runs the service_task_logs_get tool.
func (g *GetTaskLogs) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[getTaskLogsRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	params, err := parseGetTaskLogsParams(parsedRequest)
	if err != nil {
		return routing.Response{}, err
	}

	result, err := g.logs.Get(ctx, params.TaskID, tasklogs.Query{
		Limit:  params.Limit,
		Since:  params.Since,
		Until:  params.Until,
		Parse:  params.Parse,
		Levels: params.Levels,
	})
	if err != nil {
		return routing.Response{}, err
	}

	entries, truncated := boundTaskLogEntries(result.Entries)
	counts := result.Counts
	if !params.Parse {
		counts = nil
	}
	payload := struct {
		// TaskID is the target Docker task identifier.
		TaskID string `json:"task_id"`
		// Parsed reports whether structured parsing was enabled.
		Parsed bool `json:"parsed"`
		// Entries contains bounded task logs accepted by backend filtering.
		Entries []tasklogs.Entry `json:"entries"`
		// Count is the number of returned entries.
		Count int `json:"count"`
		// Truncated reports whether fields or entries were shortened to bound tool output.
		Truncated bool `json:"truncated"`
		// LevelCounts contains counts across the inspected raw window when parsing is enabled.
		LevelCounts map[tasklogs.Level]int `json:"level_counts,omitempty"`
		// AppliedSince is the applied inclusive lower boundary.
		AppliedSince string `json:"applied_since,omitempty"`
		// AppliedUntil is the applied inclusive upper boundary.
		AppliedUntil string `json:"applied_until,omitempty"`
	}{
		TaskID:       params.TaskID,
		Parsed:       params.Parse,
		Entries:      entries,
		Count:        len(entries),
		Truncated:    truncated,
		LevelCounts:  counts,
		AppliedSince: formatTimePointer(params.Since),
		AppliedUntil: formatTimePointer(params.Until),
	}

	return routing.Response{Payload: payload}, nil
}

func boundTaskLogEntries(entries []tasklogs.Entry) ([]tasklogs.Entry, bool) {
	bounded := make([]tasklogs.Entry, 0, len(entries))
	totalBytes := 0
	truncated := false

	for _, entry := range entries {
		var fieldTruncated bool
		entry.Message, fieldTruncated = truncateTaskLogText(entry.Message, maxTaskLogFieldBytes)
		truncated = truncated || fieldTruncated
		entry.RawMessage, fieldTruncated = truncateTaskLogText(entry.RawMessage, maxTaskLogFieldBytes)
		truncated = truncated || fieldTruncated

		if attributes, err := json.Marshal(entry.Attributes); err != nil || len(attributes) > maxTaskLogFieldBytes {
			entry.Attributes = map[string]any{"_truncated": true}
			truncated = true
		}

		encoded, err := json.Marshal(entry)
		if err != nil {
			truncated = true
			continue
		}
		if totalBytes+len(encoded) > maxTaskLogsPayloadBytes {
			truncated = true
			break
		}

		bounded = append(bounded, entry)
		totalBytes += len(encoded)
	}

	return bounded, truncated
}

func truncateTaskLogText(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}

	const suffix = "…[truncated]"
	limit := maxBytes - len(suffix)
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}

	return value[:limit] + suffix, true
}

type getTaskLogsParams struct {
	TaskID string
	Parse  bool
	Levels []tasklogs.Level
	Limit  int
	Since  *time.Time
	Until  *time.Time
}

func parseGetTaskLogsParams(request getTaskLogsRequest) (getTaskLogsParams, error) {
	taskID := strings.TrimSpace(request.TaskID)
	if taskID == "" {
		return getTaskLogsParams{}, fmt.Errorf("task_id is required")
	}

	limit := defaultTaskLogsLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	if limit <= 0 {
		return getTaskLogsParams{}, fmt.Errorf("limit must be > 0")
	}
	if limit > maxTaskLogsLimit {
		return getTaskLogsParams{}, fmt.Errorf("limit must be <= %d", maxTaskLogsLimit)
	}

	levels, err := tasklogs.ParseLevels(request.Levels)
	if err != nil {
		return getTaskLogsParams{}, err
	}
	if !request.Parse && len(levels) > 0 {
		return getTaskLogsParams{}, fmt.Errorf("level filters require parse=true")
	}

	sinceValue, hasSince, err := parseRFC3339TimestampParam(request.Since, "since")
	if err != nil {
		return getTaskLogsParams{}, err
	}
	var since *time.Time
	if hasSince {
		since = &sinceValue
	}

	untilValue, hasUntil, err := parseRFC3339TimestampParam(request.Until, "until")
	if err != nil {
		return getTaskLogsParams{}, err
	}
	var until *time.Time
	if hasUntil {
		until = &untilValue
	}
	if since != nil && until != nil && since.After(*until) {
		return getTaskLogsParams{}, fmt.Errorf("since must be before or equal to until")
	}

	return getTaskLogsParams{
		TaskID: taskID,
		Parse:  request.Parse,
		Levels: levels,
		Limit:  limit,
		Since:  since,
		Until:  until,
	}, nil
}
