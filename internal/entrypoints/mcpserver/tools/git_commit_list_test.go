package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListGitCommitsExecute(t *testing.T) {
	tool := NewListGitCommits(&fakeGitRepository{
		commits: []gitx.CommitMeta{
			{
				Hash:        "abc123",
				Message:     "first",
				Author:      "alice",
				AuthorEmail: "alice@example.com",
				Time:        time.Date(2026, time.March, 27, 10, 0, 0, 0, time.UTC),
			},
			{
				Hash:        "def456",
				Message:     "second",
				Author:      "bob",
				AuthorEmail: "bob@example.com",
				Time:        time.Date(2026, time.March, 27, 11, 0, 0, 0, time.UTC),
			},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"limit": float64(2),
		},
	})
	require.NoError(t, err, "execute git_commit_list")

	repository, ok := tool.repository.(*fakeGitRepository)
	require.True(t, ok, "expected fake git repository")
	assert.Equal(t, 1, repository.listCalled, "repository list should be called once")
	assert.Equal(t, 2, repository.listLimit, "unexpected repository list limit")

	var payload struct {
		Commits []gitCommitPayload `json:"commits"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")
	require.Len(t, payload.Commits, 2, "unexpected commits count")
	assert.Equal(t, "abc123", payload.Commits[0].Hash, "unexpected first commit hash")
	assert.Equal(t, "second", payload.Commits[1].Message, "unexpected second commit message")
	assert.Equal(t, "2026-03-27T11:00:00Z", payload.Commits[1].Time, "unexpected second commit time")
}

func TestListGitCommitsExecuteUsesDefaultLimit(t *testing.T) {
	tool := NewListGitCommits(&fakeGitRepository{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{},
	})
	require.NoError(t, err, "execute git_commit_list with default limit")

	repository, ok := tool.repository.(*fakeGitRepository)
	require.True(t, ok, "expected fake git repository")
	assert.Equal(t, 1, repository.listCalled, "repository list should be called once")
	assert.Equal(t, defaultGitCommitsLimit, repository.listLimit, "unexpected default limit")
}

func TestListGitCommitsExecuteFailsOnInvalidLimit(t *testing.T) {
	tool := NewListGitCommits(&fakeGitRepository{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"limit": "abc",
		},
	})
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "limit must be integer", "unexpected error")
}
