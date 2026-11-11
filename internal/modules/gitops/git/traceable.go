package git

import (
	"context"
	"net/http"

	"github.com/go-git/go-git/v5/plumbing/transport/client"
	phttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type TraceableRepository struct {
	repo   Repository
	tracer trace.Tracer
}

func NewTraceableRepository(repo Repository, tp trace.TracerProvider) Repository {
	traceGoGitHTTP()

	return &TraceableRepository{
		repo:   repo,
		tracer: tp.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"),
	}
}

func traceGoGitHTTP() {
	traceTransport := phttp.NewClient(&http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	})

	client.InstallProtocol("http", traceTransport)
	client.InstallProtocol("https", traceTransport)
}

func (t *TraceableRepository) WorkingDir() string {
	return t.repo.WorkingDir()
}

func (t *TraceableRepository) ReadFile(ctx context.Context, revision, path string) ([]byte, error) {
	ctx, span := t.tracer.Start(ctx, "repo.ReadFile", trace.WithAttributes(tracing.GitPath.String(path)))
	defer span.End()

	content, err := t.repo.ReadFile(ctx, revision, path)
	if err != nil {
		tracing.FailSpan(span, err)
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
		tracing.FailSpan(span, err)
		return result, err
	}

	span.SetAttributes(
		tracing.GitOldRevision.String(result.OldRevision),
		tracing.GitNewRevision.String(result.NewRevision),
		tracing.GitUpdated.Bool(result.Updated),
	)
	span.SetStatus(codes.Ok, "")

	return result, nil
}

func (t *TraceableRepository) Head(ctx context.Context) (string, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Head")
	defer span.End()

	head, err := t.repo.Head(ctx)
	if err != nil {
		tracing.FailSpan(span, err)
		return "", err
	}

	span.SetAttributes(tracing.GitRevision.String(head))
	span.SetStatus(codes.Ok, "")

	return head, nil
}

func (t *TraceableRepository) List(ctx context.Context, limit int) ([]CommitMeta, error) {
	ctx, span := t.tracer.Start(ctx, "repo.List", trace.WithAttributes(tracing.GitLimit.Int(limit)))
	defer span.End()

	commits, err := t.repo.List(ctx, limit)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetAttributes(tracing.GitCommitsCount.Int(len(commits)))
	span.SetStatus(codes.Ok, "")

	return commits, nil
}

func (t *TraceableRepository) Diff(
	ctx context.Context,
	oldRevision string,
	newRevision string,
) ([]CommitFileDiff, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Diff", trace.WithAttributes(
		tracing.GitOldRevision.String(oldRevision),
		tracing.GitNewRevision.String(newRevision),
	))
	defer span.End()

	fileDiffs, err := t.repo.Diff(ctx, oldRevision, newRevision)
	if err != nil {
		tracing.FailSpan(span, err)
		return nil, err
	}

	span.SetAttributes(tracing.GitFileDiffCount.Int(len(fileDiffs)))
	span.SetStatus(codes.Ok, "")

	return fileDiffs, nil
}

func (t *TraceableRepository) Show(ctx context.Context, commitHash string) (Commit, error) {
	ctx, span := t.tracer.Start(ctx, "repo.Show", trace.WithAttributes(tracing.GitCommitHash.String(commitHash)))
	defer span.End()

	commit, err := t.repo.Show(ctx, commitHash)
	if err != nil {
		tracing.FailSpan(span, err)
		return Commit{}, err
	}

	span.SetAttributes(tracing.GitFilesCount.Int(len(commit.Files)))
	span.SetStatus(codes.Ok, "")

	return commit, nil
}
