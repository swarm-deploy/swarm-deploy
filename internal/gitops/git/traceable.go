package git

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TraceableRepository struct {
	repo   Repository
	tracer trace.Tracer
}

func NewTraceableRepository(repo Repository, tp trace.TracerProvider) Repository {
	return &TraceableRepository{
		repo:   repo,
		tracer: tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/gitops/git"),
	}
}

func (t *TraceableRepository) WorkingDir() string {
	return t.repo.WorkingDir()
}

func (t *TraceableRepository) ReadFile(ctx context.Context, path string) ([]byte, error) {
	ctx, span := t.tracer.Start(ctx, "repo.ReadFile", trace.WithAttributes(attribute.String("path", path)))
	defer span.End()

	content, err := t.repo.ReadFile(ctx, path)
	if err != nil {
		t.recordError(span, err)
		return nil, err
	}

	span.SetStatus(codes.Ok, "")

	return content, nil
}

func (t *TraceableRepository) Pull(ctx context.Context) (PullResult, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Pull")
	defer span.End()

	result, err := t.repo.Pull(ctx)
	if err != nil {
		t.recordError(span, err)
		return result, err
	}

	span.SetAttributes(
		attribute.String("old_revision", result.OldRevision),
		attribute.String("new_revision", result.NewRevision),
		attribute.Bool("updated", result.Updated),
	)
	span.SetStatus(codes.Ok, "")

	return result, nil
}

func (t *TraceableRepository) Head(ctx context.Context) (string, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Head")
	defer span.End()

	head, err := t.repo.Head(ctx)
	if err != nil {
		t.recordError(span, err)
		return "", err
	}

	span.SetAttributes(attribute.String("revision", head))
	span.SetStatus(codes.Ok, "")

	return head, nil
}

func (t *TraceableRepository) List(ctx context.Context, limit int) ([]CommitMeta, error) {
	ctx, span := t.tracer.Start(ctx, "repo.List", trace.WithAttributes(attribute.Int("limit", limit)))
	defer span.End()

	commits, err := t.repo.List(ctx, limit)
	if err != nil {
		t.recordError(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("commits.count", len(commits)))
	span.SetStatus(codes.Ok, "")

	return commits, nil
}

func (t *TraceableRepository) Diff(
	ctx context.Context,
	oldRevision string,
	newRevision string,
) ([]CommitFileDiff, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Diff", trace.WithAttributes(
		attribute.String("old_revision", oldRevision),
		attribute.String("new_revision", newRevision),
	))
	defer span.End()

	fileDiffs, err := t.repo.Diff(ctx, oldRevision, newRevision)
	if err != nil {
		t.recordError(span, err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("file_diff_count", len(fileDiffs)))
	span.SetStatus(codes.Ok, "")

	return fileDiffs, nil
}

func (t *TraceableRepository) Show(ctx context.Context, commitHash string) (Commit, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Show", trace.WithAttributes(attribute.String("commit_hash", commitHash)))
	defer span.End()

	commit, err := t.repo.Show(ctx, commitHash)
	if err != nil {
		t.recordError(span, err)
		return Commit{}, err
	}

	span.SetAttributes(attribute.Int("files.count", len(commit.Files)))
	span.SetStatus(codes.Ok, "")

	return commit, nil
}

func (t *TraceableRepository) recordError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
