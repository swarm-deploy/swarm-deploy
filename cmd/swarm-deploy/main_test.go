package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/cappuccinotm/slogx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sharedtracing "github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/trace"
)

func TestLoggingMiddlewaresExcludeTraceIDWhenTracingDisabled(t *testing.T) {
	require.False(t, sharedtracing.Enabled())

	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
	}))

	var output bytes.Buffer
	logger := slog.New(slogx.NewChain(
		slog.NewJSONHandler(&output, nil),
		loggingMiddlewares()...,
	))

	logger.InfoContext(ctx, "message")

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.NotContains(t, record, "trace-id")
}

func TestRepositoryLogValueRemovesCredentialsFromMalformedURL(t *testing.T) {
	const secret = "top-secret-token"
	value := repositoryLogValue("https://robot:" + secret + "%zz@git.example.com/team/app.git")

	assert.NotContains(t, value, secret)
	assert.NotContains(t, value, "robot")
	assert.Regexp(t, `^repository-[0-9a-f]{16}$`, value)
}
