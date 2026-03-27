package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// Date returns current time for the requested timezone.
type Date struct {
	now func() time.Time
}

// NewDate creates date component.
func NewDate() *Date {
	return &Date{
		now: time.Now,
	}
}

// Definition returns tool metadata visible to the model.
func (d *Date) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "date",
		Description: "Returns current time in UTC or in requested IANA timezone.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "Optional IANA timezone name, for example Europe/Moscow.",
				},
			},
		},
	}
}

// Execute runs date tool.
func (d *Date) Execute(_ context.Context, request routing.Request) (routing.Response, error) {
	location, err := parseCurrentTimeLocation(request.Payload["timezone"])
	if err != nil {
		return routing.Response{}, err
	}

	currentTime := d.now().In(location)
	weekdayISO := int(currentTime.Weekday())
	if weekdayISO == 0 {
		weekdayISO = 7
	}

	payload := struct {
		Time       string `json:"time"`
		Unix       int64  `json:"unix"`
		Timezone   string `json:"timezone"`
		Weekday    string `json:"weekday"`
		WeekdayISO int    `json:"weekdayIso"`
	}{
		Time:       currentTime.Format(time.RFC3339),
		Unix:       currentTime.Unix(),
		Timezone:   location.String(),
		Weekday:    currentTime.Weekday().String(),
		WeekdayISO: weekdayISO,
	}

	return routing.Response{
		Payload: payload,
	}, nil
}

func parseCurrentTimeLocation(raw any) (*time.Location, error) {
	if raw == nil {
		return time.UTC, nil
	}

	timezone, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("timezone must be string")
	}

	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return time.UTC, nil
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", timezone, err)
	}

	return location, nil
}
