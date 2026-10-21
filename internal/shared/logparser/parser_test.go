package logparser

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
			name:        "python structlog event",
			message:     `{"timestamp":"2026-10-19T18:29:57Z","level":"warning","event":"quota nearing limit","logger":"billing","account_id":"acct-1"}`,
			wantTime:    time.Date(2026, time.October, 19, 18, 29, 57, 0, time.UTC),
			wantLevel:   LevelWarn,
			wantMessage: "quota nearing limit",
			wantAttrs: map[string]any{
				"logger":     "billing",
				"account_id": "acct-1",
			},
		},
		{
			name:        "serilog compact json",
			message:     `{"@t":"2026-10-19T18:29:56.123Z","@l":"Error","@m":"Request failed","RequestId":"req-3","Elapsed":12.5}`,
			wantTime:    time.Date(2026, time.October, 19, 18, 29, 56, 123000000, time.UTC),
			wantLevel:   LevelError,
			wantMessage: "Request failed",
			wantAttrs: map[string]any{
				"RequestId": "req-3",
				"Elapsed":   requireJSONNumber(t, "12.5"),
			},
		},
		{
			name:        "ecs nested log level",
			message:     `{"@timestamp":"2026-10-19T18:29:55Z","log":{"level":"error","logger":"checkout"},"message":"payment failed","service":{"name":"api"},"trace.id":"abc"}`,
			wantTime:    time.Date(2026, time.October, 19, 18, 29, 55, 0, time.UTC),
			wantLevel:   LevelError,
			wantMessage: "payment failed",
			wantAttrs: map[string]any{
				"log":      map[string]any{"logger": "checkout"},
				"service":  map[string]any{"name": "api"},
				"trace.id": "abc",
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
			name:        "spring boot error",
			message:     "2026-10-20T09:12:33.123Z ERROR 1 --- [nio-8080-exec-1] c.e.orders.OrderController : Request failed",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelError,
			wantMessage: "2026-10-20T09:12:33.123Z ERROR 1 --- [nio-8080-exec-1] c.e.orders.OrderController : Request failed",
		},
		{
			name:        "python logging default error",
			message:     "ERROR:worker.jobs:database connection failed",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelError,
			wantMessage: "ERROR:worker.jobs:database connection failed",
		},
		{
			name:        "python logging formatted critical",
			message:     "2026-10-20 09:12:33,123 - worker.jobs - CRITICAL - queue unavailable",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelFatal,
			wantMessage: "2026-10-20 09:12:33,123 - worker.jobs - CRITICAL - queue unavailable",
		},
		{
			name:        "nginx error log",
			message:     "2026/10/20 09:12:33 [error] 123#123: *1 connect() failed (111: Connection refused) while connecting to upstream",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelError,
			wantMessage: "2026/10/20 09:12:33 [error] 123#123: *1 connect() failed (111: Connection refused) while connecting to upstream",
		},
		{
			name:        "plain text error words are not enough",
			message:     "request completed status=500 error_count=1 selected=[error]",
			wantTime:    dockerTimestamp,
			wantLevel:   LevelInfo,
			wantMessage: "request completed status=500 error_count=1 selected=[error]",
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

func TestParserParsePinoAndBunyanNumericLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		message     string
		wantLevel   Level
		wantMessage string
		wantAttrs   map[string]any
	}{
		{
			name:        "pino trace",
			message:     `{"level":10,"time":1792440000123,"pid":42,"hostname":"api-1","msg":"request received"}`,
			wantLevel:   LevelTrace,
			wantMessage: "request received",
			wantAttrs:   map[string]any{"pid": requireJSONNumber(t, "42"), "hostname": "api-1"},
		},
		{
			name:        "bunyan debug",
			message:     `{"name":"worker","hostname":"node-1","pid":43,"level":20,"msg":"polling queue","time":"2026-10-19T18:30:00Z","v":0}`,
			wantLevel:   LevelDebug,
			wantMessage: "polling queue",
			wantAttrs: map[string]any{
				"name": "worker", "hostname": "node-1", "pid": requireJSONNumber(t, "43"), "v": requireJSONNumber(t, "0"),
			},
		},
		{
			name:        "pino info",
			message:     `{"level":30,"time":1792440000123,"pid":44,"hostname":"api-1","msg":"request completed"}`,
			wantLevel:   LevelInfo,
			wantMessage: "request completed",
			wantAttrs:   map[string]any{"pid": requireJSONNumber(t, "44"), "hostname": "api-1"},
		},
		{
			name:        "bunyan warn",
			message:     `{"name":"worker","hostname":"node-1","pid":45,"level":40,"msg":"retrying job","time":"2026-10-19T18:30:00Z","v":0}`,
			wantLevel:   LevelWarn,
			wantMessage: "retrying job",
			wantAttrs: map[string]any{
				"name": "worker", "hostname": "node-1", "pid": requireJSONNumber(t, "45"), "v": requireJSONNumber(t, "0"),
			},
		},
		{
			name:        "pino error",
			message:     `{"level":50,"time":1792440000123,"pid":46,"hostname":"api-1","msg":"request failed","err":{"type":"Error","message":"boom"}}`,
			wantLevel:   LevelError,
			wantMessage: "request failed",
			wantAttrs: map[string]any{
				"pid": requireJSONNumber(t, "46"), "hostname": "api-1", "err": map[string]any{"type": "Error", "message": "boom"},
			},
		},
		{
			name:        "bunyan fatal",
			message:     `{"name":"worker","hostname":"node-1","pid":47,"level":60,"msg":"uncaught exception","time":"2026-10-19T18:30:00Z","v":0}`,
			wantLevel:   LevelFatal,
			wantMessage: "uncaught exception",
			wantAttrs: map[string]any{
				"name": "worker", "hostname": "node-1", "pid": requireJSONNumber(t, "47"), "v": requireJSONNumber(t, "0"),
			},
		},
	}

	parser := NewParser()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			entry := parser.Parse(RawEntry{
				Timestamp: time.Date(2026, time.October, 19, 18, 30, 0, 0, time.UTC),
				Stream:    "stdout",
				Message:   test.message,
			})

			assert.Equal(t, test.wantLevel, entry.Level, "unexpected normalized level")
			assert.Equal(t, test.wantMessage, entry.Message, "framework message must be preserved")
			assert.Equal(t, test.wantAttrs, entry.Attributes, "framework attributes must be preserved")
			assert.Equal(t, "stdout", entry.Stream, "Docker stream metadata must be preserved")
			assert.Equal(t, test.message, entry.RawMessage, "raw message must be preserved")
		})
	}
}

func TestParserRejectsOtherNumericFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
	}{
		{name: "non standard level", message: `{"level":35,"message":"custom level"}`},
		{name: "http status as level", message: `{"level":500,"message":"upstream response"}`},
		{name: "decimal representation", message: `{"level":50.0,"message":"decimal"}`},
		{name: "exponent representation", message: `{"level":5e1,"message":"exponent"}`},
		{name: "numeric string", message: `{"level":"50","message":"string"}`},
		{name: "unrelated numeric field", message: `{"status":50,"message":"request complete"}`},
	}

	parser := NewParser()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			entry := parser.Parse(RawEntry{Message: test.message})

			assert.Equal(t, LevelInfo, entry.Level, "other numeric formats must not be treated as severity")
			assert.Equal(t, test.message, entry.RawMessage, "raw message must be preserved")
		})
	}
}

func TestParseLevels(t *testing.T) {
	t.Parallel()

	levels, err := ParseLevels([]string{"warning", "ERROR", "warn"})
	require.NoError(t, err)
	assert.Equal(t, []Level{LevelWarn, LevelError}, levels)

	_, err = ParseLevels([]string{"custom"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported log level")
}

func requireJSONNumber(t *testing.T, value string) any {
	t.Helper()

	fields, ok := parseJSONFields(`{"value":` + value + `}`)
	require.True(t, ok)

	return fields["value"]
}
