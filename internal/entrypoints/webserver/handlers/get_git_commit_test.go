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
	"go.uber.org/mock/gomock"
)

func TestHandlerGetGitCommitReturnsCommitMetadata(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	repo := gitx.NewMockRepository(ctrl)
	commitDate := time.Date(2026, time.April, 25, 1, 2, 3, 0, time.UTC)
	commitHash := "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc"
	repo.EXPECT().
		Show(gomock.Any(), commitHash).
		Return(gitx.Commit{
			Author:  "alice",
			Message: "second commit",
			Time:    commitDate,
			Files: []gitx.CommitFileDiff{
				{NewPath: "README.md"},
				{OldPath: "docker-compose.yaml"},
				{NewPath: "README.md"},
			},
		}, nil)

	h := &handler{
		git: repo,
	}

	resp, err := h.GetGitCommit(context.Background(), generated.GetGitCommitParams{
		Commit: commitHash,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, commitHash, resp.FullHash)
	assert.Equal(t, "alice", resp.Author)
	assert.Equal(t, "second commit", resp.Message)
	assert.Equal(t, commitDate.Unix(), resp.Date.Unix())
	assert.Equal(t, []string{"README.md", "docker-compose.yaml"}, resp.ChangedFiles)
}

func TestHandlerGetGitCommitReturns404WhenCommitMissing(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	repo := gitx.NewMockRepository(ctrl)
	commitHash := "4bd9beaa8f7f5737f73d8f92de130f7ec32f07cc"
	repo.EXPECT().
		Show(gomock.Any(), commitHash).
		Return(gitx.Commit{}, fmt.Errorf("find commit: %w", plumbing.ErrObjectNotFound))

	h := &handler{
		git: repo,
	}

	_, err := h.GetGitCommit(context.Background(), generated.GetGitCommitParams{
		Commit: commitHash,
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
}

func TestHandlerGetGitCommitReturns400OnEmptyHash(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	repo := gitx.NewMockRepository(ctrl)
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
