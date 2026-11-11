package fs

import (
	"context"
	"os"

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
		tracing.FilePath.String(path),
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
		tracing.FilePath.String(path),
		tracing.FileMode.String(mode.String()),
		tracing.FileSize.Int(len(payload)),
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
		tracing.FilePath.String(path),
		tracing.FileMode.String(perm.String()),
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
		tracing.FileOldPath.String(oldPath),
		tracing.FileNewPath.String(newPath),
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

func (s *TraceableFileSystem) RemoveFile(ctx context.Context, path string) error {
	ctx, span := s.tracer.Start(ctx, "fs.RemoveFile", trace.WithAttributes(
		tracing.FilePath.String(path),
	))
	defer span.End()

	err := s.fs.RemoveFile(ctx, path)
	if err != nil {
		tracing.FailSpan(span, err)

		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
