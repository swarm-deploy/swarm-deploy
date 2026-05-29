package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
)

type fakeCommitRepository struct {
	commit   gitx.Commit
	showErr  error
	showHash string
}

func (f *fakeCommitRepository) Pull(context.Context) (gitx.PullResult, error) {
	return gitx.PullResult{}, nil
}

func (f *fakeCommitRepository) Head(context.Context) (string, error) {
	return "", nil
}

func (f *fakeCommitRepository) List(context.Context, int) ([]gitx.CommitMeta, error) {
	return nil, nil
}

func (f *fakeCommitRepository) Show(_ context.Context, commitHash string) (gitx.Commit, error) {
	f.showHash = commitHash
	if f.showErr != nil {
		return gitx.Commit{}, f.showErr
	}

	return f.commit, nil
}

func (f *fakeCommitRepository) WorkingDir() string {
	return ""
}

func TestHandlerGetGitCommitReturnsCommitMetadata(t *testing.T) {
	t.Parallel()

	commitDate := time.Date(2026, time.April, 25, 1, 2, 3, 0, time.UTC)
	repo := &fakeCommitRepository{
		commit: gitx.Commit{
			Author:  "alice",
			Message: "second commit",
			Time:    commitDate,
			Files: []gitx.CommitFileDiff{
				{NewPath: "README.md"},
				{OldPath: "docker-compose.yaml"},
				{NewPath: "README.md"},
			},
		},
	}
	h := &handler{
		git: repo,
	}

	resp, err := h.GetGitCommit(context.Background(), generated.GetGitCommitParams{
		Commit: "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc", repo.showHash)
	assert.Equal(t, "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc", resp.FullHash)
	assert.Equal(t, "alice", resp.Author)
	assert.Equal(t, "second commit", resp.Message)
	assert.Equal(t, commitDate.Unix(), resp.Date.Unix())
	assert.Equal(t, []string{"README.md", "docker-compose.yaml"}, resp.ChangedFiles)
}

func TestHandlerGetGitCommitReturns404WhenCommitMissing(t *testing.T) {
	t.Parallel()

	repo := &fakeCommitRepository{
		showErr: fmt.Errorf("find commit: %w", plumbing.ErrObjectNotFound),
	}
	h := &handler{
		git: repo,
	}

	_, err := h.GetGitCommit(context.Background(), generated.GetGitCommitParams{
		Commit: "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
}

func TestHandlerGetGitCommitReturns400OnEmptyHash(t *testing.T) {
	t.Parallel()

	repo := &fakeCommitRepository{}
	h := &handler{
		git: repo,
	}

	_, err := h.GetGitCommit(context.Background(), generated.GetGitCommitParams{
		Commit: "   ",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 400, statusErr.code)
}
