package faults

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "docker detail", err: errors.New("update service api: rpc error: image not found"),
			want: "update service api: rpc error: image not found"},
		{name: "inline token", err: errors.New("registry request failed: token=private-value"),
			want: "registry request failed: token=****"},
		{name: "credential URL", err: errors.New("pull https://user:password@registry.example/image failed"),
			want: "pull https://****:****@registry.example/image failed"},
		{name: "empty", err: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeDiagnostic(tt.err))
		})
	}
}

func TestSafeDiagnosticIsBounded(t *testing.T) {
	result := SafeDiagnostic(errors.New(strings.Repeat("x", maxDiagnosticLength+100)))
	assert.LessOrEqual(t, len(result), maxDiagnosticLength+len("…"))
}

func TestSafeLoggerMasksErrorAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := NewSafeLogger(slog.New(slog.NewJSONHandler(&output, nil)))
	logger.ErrorContext(context.Background(), "apply failed",
		"err", errors.New("pull https://user:password@registry.example/image token=private-value"))
	assert.NotContains(t, output.String(), "password")
	assert.NotContains(t, output.String(), "private-value")
	assert.Contains(t, output.String(), "****")
}

func TestSafeErrorPreservesClassificationWithoutRetainingCause(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		original := &net.DNSError{Err: "token=private-value", IsTemporary: temporary}
		safe := SafeError(fmt.Errorf("lookup: %w", original))
		assert.Equal(t, SafeDiagnostic(original), strings.TrimPrefix(safe.Error(), "lookup: "))
		assert.Equal(t, temporary, safe.(Error).Temporary())
		assert.NotContains(t, safe.Error(), "private-value")
		assert.Nil(t, errors.Unwrap(safe))
		assert.False(t, errors.Is(safe, original))
	}
	assert.Nil(t, SafeError(nil))
	assert.False(t, SafeError(errors.New("permanent")).(Error).Temporary())
	assert.True(t, SafeError(&net.DNSError{IsTemporary: true}).(Error).Temporary())
}
