package tracing

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestLogTraceID(t *testing.T) {
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	tests := []struct {
		name      string
		ctx       context.Context
		wantValue string
		wantAttr  bool
	}{
		{
			name: "valid span context",
			ctx: trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
			})),
			wantValue: traceID.String(),
			wantAttr:  true,
		},
		{
			name: "no span context",
			ctx:  context.Background(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotRecord slog.Record
			called := false
			handle := LogTraceID()(func(_ context.Context, rec slog.Record) error {
				called = true
				gotRecord = rec
				return nil
			})

			err := handle(tt.ctx, slog.Record{})
			require.NoError(t, err)
			require.True(t, called)

			gotValue, gotAttr := recordAttribute(gotRecord, traceIDLogAttribute)
			assert.Equal(t, tt.wantAttr, gotAttr)
			assert.Equal(t, tt.wantValue, gotValue)
		})
	}
}

func recordAttribute(record slog.Record, key string) (string, bool) {
	var value string
	var found bool
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != key {
			return true
		}
		value = attr.Value.String()
		found = true
		return false
	})
	return value, found
}
