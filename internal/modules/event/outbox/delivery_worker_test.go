package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

type safeTestError struct{}

func (safeTestError) Error() string { return "token=private-value" }
func (safeTestError) SafeLogFields() []slog.Attr {
	return []slog.Attr{slog.String("failure_source", "telegram"), slog.Int("http_status", 429)}
}

func TestSafeDeliveryErrorFields(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantSource string
		wantKind   string
	}{
		{"unknown secret", errors.New("token=private-value"), "handler", "unclassified"},
		{"deadline", context.DeadlineExceeded, "runtime", "deadline_exceeded"},
		{"sqlite", &sqlite.Error{}, "sqlite", "database_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := safeDeliveryErrorFields(tt.err)
			got := map[string]any{}
			for i := 0; i+1 < len(fields); i += 2 {
				got[fields[i].(string)] = fields[i+1]
			}
			assert.Equal(t, tt.wantSource, got["failure_source"])
			assert.Equal(t, tt.wantKind, got["failure_kind"])
			assert.NotContains(t, got, "error")
		})
	}
	fields := safeDeliveryErrorFields(errors.Join(errors.New("token=private-value"), safeTestError{}))
	require.Len(t, fields, 2)
	assert.Equal(t, "failure_source=telegram", fields[0].(slog.Attr).String())
	assert.Equal(t, "http_status=429", fields[1].(slog.Attr).String())
}
