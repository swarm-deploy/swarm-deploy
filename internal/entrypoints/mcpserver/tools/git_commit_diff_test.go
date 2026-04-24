package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/differ"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/git"
)

func TestGitCommitDiffExecute(t *testing.T) {
	repository := &fakeGitRepository{
		commit: gitx.Commit{
			Author:      "alice",
			AuthorEmail: "alice@example.com",
			Time:        defaultCommitTime(),
			Files: []gitx.CommitFileDiff{
				{
					NewPath:    "deploy/app.yaml",
					OldContent: "services:\n  api:\n    image: ghcr.io/acme/api:1.0.0\n",
					NewContent: "services:\n  api:\n    image: ghcr.io/acme/api:2.0.0\n",
				},
				{
					NewPath: "README.md",
				},
			},
		},
	}

	composeDiffer := &fakeCommitDiffer{
		diff: differ.Diff{
			Services: []differ.ServiceDiff{{
				ServiceName: "api",
				StackName:   "core",
				Environment: []differ.EnvironmentDiff{
					{
						VarName: "API_KEY",
						Value:   "super-secret",
						Changed: true,
					},
				},
			}},
		},
	}

	tool := NewGitCommitDiff(repository, []config.StackSpec{
		{
			Name:        "core",
			ComposeFile: "deploy/app.yaml",
		},
	}, composeDiffer)

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"commit": "abc123",
		},
	})
	require.NoError(t, err, "execute git_commit_diff")
	assert.Equal(t, 1, repository.showCalled, "repository show should be called once")
	assert.Equal(t, "abc123", repository.showHash, "unexpected repository hash")
	assert.Equal(t, 1, composeDiffer.called, "compose differ should be called once")
	require.Len(t, composeDiffer.composeFiles, 1, "only stack compose file should be passed into differ")
	assert.Equal(t, "core", composeDiffer.composeFiles[0].StackName, "unexpected compose stack")
	assert.Equal(t, "deploy/app.yaml", composeDiffer.composeFiles[0].ComposePath, "unexpected compose path")

	var payload struct {
		Commit      string      `json:"commit"`
		Author      string      `json:"author"`
		AuthorEmail string      `json:"authorEmail"`
		Time        string      `json:"time"`
		Diff        differ.Diff `json:"diff"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	assert.Equal(t, "abc123", payload.Commit, "unexpected payload commit")
	assert.Equal(t, "alice", payload.Author, "unexpected payload author")
	assert.Equal(t, "alice@example.com", payload.AuthorEmail, "unexpected payload author email")
	assert.Equal(t, "2026-03-27T00:00:00Z", payload.Time, "unexpected payload time")
	require.Len(t, payload.Diff.Services, 1, "unexpected services count")
	assert.Equal(t, "api", payload.Diff.Services[0].ServiceName, "unexpected service in payload")
	require.Len(t, payload.Diff.Services[0].Environment, 1, "unexpected environment diff count")
	assert.Equal(t, "API_KEY", payload.Diff.Services[0].Environment[0].VarName, "unexpected env var name")
}

func TestGitCommitDiffExecuteFailsOnMissingCommit(t *testing.T) {
	tool := NewGitCommitDiff(&fakeGitRepository{}, nil, &fakeCommitDiffer{})

	_, err := tool.Execute(context.Background(), routing.Request{Payload: map[string]any{}})
	require.Error(t, err, "commit is required")
	assert.Contains(t, err.Error(), "commit is required", "unexpected error")
}

func TestGitCommitDiffExecuteFailsOnRepositoryError(t *testing.T) {
	tool := NewGitCommitDiff(&fakeGitRepository{err: errors.New("boom")}, nil, &fakeCommitDiffer{})

	_, err := tool.Execute(context.Background(), routing.Request{Payload: map[string]any{"commit": "abc123"}})
	require.Error(t, err, "repository error must be returned")
	assert.Contains(t, err.Error(), "boom", "unexpected error")
}

func TestGitCommitDiffExecuteFailsOnDifferError(t *testing.T) {
	repository := &fakeGitRepository{
		commit: gitx.Commit{Time: defaultCommitTime()},
	}
	tool := NewGitCommitDiff(repository, nil, &fakeCommitDiffer{err: errors.New("diff failed")})

	_, err := tool.Execute(context.Background(), routing.Request{Payload: map[string]any{"commit": "abc123"}})
	require.Error(t, err, "differ error must be returned")
	assert.Contains(t, err.Error(), "diff failed", "unexpected error")
}
