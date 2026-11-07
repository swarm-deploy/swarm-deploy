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
	"github.com/stretchr/testify/require"
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
		{name: "quoted password", err: errors.New(`registry failed: PASSWORD: "a password with spaces" (denied)`),
			want: "registry failed: PASSWORD: **** (denied)"},
		{name: "pipeline key aliases", err: errors.New("passwd=value credential=value api_key=value"),
			want: "passwd=**** credential=**** api_key=****"},
		{name: "CLI flag and prose", err: errors.New("cannot read secret file (with --token private-value)"),
			want: "cannot read secret file (with --token ****)"},
		{name: "leading secret field", err: errors.New("secret private-value"), want: "secret ****"},
		{name: "secret failure detail", err: errors.New("cannot decrypt age secret: invalid ciphertext"),
			want: "cannot decrypt age secret: invalid ciphertext"},
		{name: "secret resource detail", err: errors.New("create secret demo-password: Docker API request failed"),
			want: "create secret ****: Docker API request failed"},
		{name: "bare DSN with port", err: errors.New("dial (user:pass@db.example:5432/app): connection refused"),
			want: "dial (****:****@db.example:5432/app): connection refused"},
		{name: "bare DSN before punctuation", err: errors.New("dial user:pass@db.example: connection refused"),
			want: "dial ****:****@db.example: connection refused"},
		{name: "image digest", err: errors.New("pull registry:5000/app:stable@sha256:abc123: image not found"),
			want: "pull registry:5000/app:stable@sha256:abc123: image not found"},
		{name: "URL query", err: errors.New("GET https://registry.example/path?token=value&state=pending failed"),
			want: "GET https://registry.example/path?token=****&state=pending failed"},
		{name: "slog config failure", err: errors.New(`level=ERROR msg="failed to load config" err="unknown configuration field"`),
			want: `level=ERROR msg="failed to load config" err="unknown configuration field"`},
		{name: "slog secret field", err: errors.New(`err="unknown configuration field" api_key="private value"`),
			want: `err="unknown configuration field" api_key=****`},
		{name: "quoted secret failure detail", err: errors.New(`err="cannot read secret file"`),
			want: `err="cannot read secret file"`},
		{name: "authorization header", err: errors.New("request failed Authorization: Bearer private-value status=401"),
			want: "request failed Authorization: **** status=401"},
		{name: "quoted DSN", err: errors.New(`connect dsn="host=db password=private-value" failed`),
			want: "connect dsn=**** failed"},
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

type codedTestError struct {
	*net.DNSError
	summary string
}

func (*codedTestError) FaultCode() Code { return CodeImageNotFound }

func (e *codedTestError) FaultSummary() string { return e.summary }

func TestSafeErrorPreservesFaultDetailsWithoutRetainingCause(t *testing.T) {
	original := &codedTestError{
		DNSError: &net.DNSError{Err: "token=private-value", IsTemporary: true},
		summary:  "No such image: app:1 token=summary-secret " + strings.Repeat("x", maxDiagnosticLength),
	}
	safe := SafeError(fmt.Errorf("lookup: %w", original))
	var coded Coded
	require.ErrorAs(t, safe, &coded)
	assert.Equal(t, CodeImageNotFound, coded.FaultCode())
	assert.Equal(t, ("No such image: app:1 token=**** " + strings.Repeat("x", maxDiagnosticLength))[:maxDiagnosticLength]+"…",
		coded.FaultSummary())
	assert.True(t, safe.(Error).Temporary())
	assert.Contains(t, safe.Error(), "token=****")
	assert.NotContains(t, safe.Error(), "private-value")
	assert.NotErrorIs(t, safe, original)
	assert.Nil(t, errors.Unwrap(safe))
	assert.NotErrorAs(t, SafeError(errors.New("permanent")), &coded)
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
