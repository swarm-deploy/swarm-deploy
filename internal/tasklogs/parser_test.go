package tasklogs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParserParse(t *testing.T) {
	t.Parallel()

	dockerTimestamp := time.Date(2026, time.October, 19, 18, 30, 0, 0, time.UTC)
	tests := []struct {
		name        string
		message     string
		wantTime    time.Time
		wantLevel   Level
		wantMessage string
		wantAttrs   map[string]any
	}{
		{
			name:        "json structured log",
			message:     `{"time":"2026-10-19T18:29:59.123Z","level":"warning","msg":"request slow","request_id":"req-1","duration_ms":42}`,
			wantTime:    time.Date(2026, time.October, 19, 18, 29, 59, 123000000, time.UTC),
			wantLevel:   LevelWarn,
			wantMessage: "request slow",
			wantAttrs: map[string]any{
				"request_id":  "req-1",
				"duration_ms": requireJSONNumber(t, "42"),
			},
		},
		{
			name:        "logfmt structured log",
			message:     `ts=2026-10-19T18:29:58Z level=err msg="request failed" request_id=req-2 retry=true`,
			wantTime:    time.Date(2026, time.October, 19, 18, 29, 58, 0, time.UTC),
			wantLevel:   LevelError,
			wantMessage: "request failed",
			wantAttrs: map[string]any{
				"request_id": "req-2",
				"retry":      "true",
			},
		},
		{
			name:        "plain text fallback",
			message:     "worker started",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelInfo,
			wantMessage: "worker started",
		},
		{
			name:        "malformed json fallback",
			message:     `{"level":"error","message":"broken"`,
			wantTime:    dockerTimestamp,
			wantLevel:   LevelInfo,
			wantMessage: `{"level":"error","message":"broken"`,
		},
		{
			name:        "malformed logfmt fallback",
			message:     `level=error msg="unterminated`,
			wantTime:    dockerTimestamp,
			wantLevel:   LevelInfo,
			wantMessage: `level=error msg="unterminated`,
		},
		{
			name:        "unknown structured level uses info",
			message:     `{"severity":"custom","message":"hello","component":"api"}`,
			wantTime:    dockerTimestamp,
			wantLevel:   LevelInfo,
			wantMessage: "hello",
			wantAttrs:   map[string]any{"component": "api"},
		},
	}

	parser := NewParser()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			entry := parser.Parse(RawEntry{
				Timestamp: dockerTimestamp,
				Stream:    "stderr",
				Message:   test.message,
			})

			assert.Equal(t, test.wantTime, entry.Timestamp, "unexpected timestamp")
			assert.Equal(t, "stderr", entry.Stream, "Docker stream metadata must be preserved")
			assert.Equal(t, test.wantLevel, entry.Level, "unexpected normalized level")
			assert.Equal(t, test.wantMessage, entry.Message, "unexpected normalized message")
			assert.Equal(t, test.wantAttrs, entry.Attributes, "unexpected attributes")
			assert.Equal(t, test.message, entry.RawMessage, "raw message must be preserved")
		})
	}
}

func TestParseLevels(t *testing.T) {
	t.Parallel()

	levels, err := ParseLevels([]string{"warning", "ERROR", "warn"})
	require.NoError(t, err)
	assert.Equal(t, []Level{LevelWarn, LevelError}, levels)

	_, err = ParseLevels([]string{"verbose"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported log level")
}

func requireJSONNumber(t *testing.T, value string) any {
	t.Helper()

	fields, ok := parseJSONFields(`{"value":` + value + `}`)
	require.True(t, ok)

	return fields["value"]
}
