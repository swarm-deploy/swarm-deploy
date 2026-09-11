package fs

import (
	"context"
	"os"

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

func (s *TraceableFileSystem) WriteFile(ctx context.Context, path string, payload []byte, mode os.FileMode) error {
	ctx, span := s.tracer.Start(ctx, "fs.WriteFile", trace.WithAttributes(
		attribute.String("file.path", path),
		attribute.String("file.mode", mode.String()),
		attribute.Int("file.size", len(payload)),
	))
	defer span.End()

	err := s.fs.WriteFile(ctx, path, payload, mode)
	if err != nil {
		tracing.FailSpan(span, err)

		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (s *TraceableFileSystem) CreateDirectory(ctx context.Context, path string, perm os.FileMode) error {
	ctx, span := s.tracer.Start(ctx, "fs.CreateDirectory", trace.WithAttributes(
		attribute.String("file.path", path),
		attribute.String("file.mode", perm.String()),
	))
	defer span.End()

	err := s.fs.CreateDirectory(ctx, path, perm)
	if err != nil {
		tracing.FailSpan(span, err)

		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

func (s *TraceableFileSystem) Rename(ctx context.Context, oldPath string, newPath string) error {
	ctx, span := s.tracer.Start(ctx, "fs.Rename", trace.WithAttributes(
		attribute.String("file.old_path", oldPath),
		attribute.String("file.new_path", newPath),
	))
	defer span.End()

	err := s.fs.Rename(ctx, oldPath, newPath)
	if err != nil {
		tracing.FailSpan(span, err)

		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
