package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDateExecuteDefaultUTC(t *testing.T) {
	tool := NewDate()
	tool.now = func() time.Time {
		return time.Date(2026, time.March, 27, 10, 15, 30, 0, time.FixedZone("UTC+03", 3*60*60))
	}

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute date")

	var payload struct {
		Time       string `json:"time"`
		Unix       int64  `json:"unix"`
		Timezone   string `json:"timezone"`
		Weekday    string `json:"weekday"`
		WeekdayISO int    `json:"weekdayIso"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "2026-03-27T07:15:30Z", payload.Time, "unexpected time")
	assert.Equal(t, int64(1774595730), payload.Unix, "unexpected unix")
	assert.Equal(t, "UTC", payload.Timezone, "unexpected timezone")
	assert.Equal(t, "Friday", payload.Weekday, "unexpected weekday")
	assert.Equal(t, 5, payload.WeekdayISO, "unexpected weekday iso")
}

func TestDateExecuteWithTimezone(t *testing.T) {
	tool := NewDate()
	tool.now = func() time.Time {
		return time.Date(2026, time.March, 27, 7, 15, 30, 0, time.UTC)
	}

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"timezone": "Europe/Moscow",
		},
	})
	require.NoError(t, err, "execute date")

	var payload struct {
		Time       string `json:"time"`
		Unix       int64  `json:"unix"`
		Timezone   string `json:"timezone"`
		Weekday    string `json:"weekday"`
		WeekdayISO int    `json:"weekdayIso"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "2026-03-27T10:15:30+03:00", payload.Time, "unexpected time")
	assert.Equal(t, int64(1774595730), payload.Unix, "unexpected unix")
	assert.Equal(t, "Europe/Moscow", payload.Timezone, "unexpected timezone")
	assert.Equal(t, "Friday", payload.Weekday, "unexpected weekday")
	assert.Equal(t, 5, payload.WeekdayISO, "unexpected weekday iso")
}

func TestDateExecuteInvalidTimezoneType(t *testing.T) {
	tool := NewDate()

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"timezone": 123,
		},
	})
	require.Error(t, err, "expected timezone type error")
	assert.Contains(t, err.Error(), "timezone must be string", "unexpected error")
}

func TestDateExecuteInvalidTimezone(t *testing.T) {
	tool := NewDate()

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"timezone": "Mars/Colony-1",
		},
	})
	require.Error(t, err, "expected timezone load error")
	assert.Contains(t, err.Error(), "load timezone", "unexpected error")
}
