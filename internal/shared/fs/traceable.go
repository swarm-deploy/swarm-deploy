package fs

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type TraceableFileSystem struct {
	tracer trace.Tracer
	fs     FileSystem
}

func NewTraceableFileSystem(tp trace.TracerProvider, fs FileSystem) *TraceableFileSystem {
	return &TraceableFileSystem{
		tracer: tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/shared/traceos"),
		fs:     fs,
	}
}

func TraceOS() FileSystem {
	tp, enabled := tracing.GetTracerProvider()
	if !enabled {
		return NewLocalFileSystem()
	}

	return NewTraceableFileSystem(tp, NewLocalFileSystem())
}

func (s *TraceableFileSystem) ReadFile(ctx context.Context, path string) ([]byte, error) {
	ctx, span := s.tracer.Start(ctx, "fs.ReadFile", trace.WithAttributes(
		attribute.String("file.path", path),
	))
	defer span.End()

	content, err := s.fs.ReadFile(ctx, path)
	if err != nil {
		tracing.FailSpan(span, err)

		return content, err
	}

	span.SetStatus(codes.Ok, "")

	return content, nil
}
