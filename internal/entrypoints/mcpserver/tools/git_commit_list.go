package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

const (
	defaultGitCommitsLimit = 10
	maxGitCommitsLimit     = 100
)

// ListGitCommits returns latest commits from git repository.
type ListGitCommits struct {
	repository GitRepository
}

// NewListGitCommits creates git_commit_list component.
func NewListGitCommits(repository GitRepository) *ListGitCommits {
	return &ListGitCommits{
		repository: repository,
	}
}

// Definition returns tool metadata visible to the model.
func (l *ListGitCommits) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "git_commit_list",
		Description: "Returns latest commits from repository history.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     maxGitCommitsLimit,
					"description": "Maximum number of latest commits to return.",
				},
			},
		},
	}
}

// Execute runs git_commit_list tool.
func (l *ListGitCommits) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	limit, err := parseGitCommitsLimit(request.Payload["limit"])
	if err != nil {
		return routing.Response{}, err
	}

	commits, err := l.repository.List(ctx, limit)
	if err != nil {
		return routing.Response{}, err
	}

	payload := struct {
		Commits []gitCommitPayload `json:"commits"`
	}{
		Commits: make([]gitCommitPayload, 0, len(commits)),
	}
	for _, commit := range commits {
		payload.Commits = append(payload.Commits, gitCommitPayload{
			Hash:        commit.Hash,
			Message:     commit.Message,
			Author:      commit.Author,
			AuthorEmail: commit.AuthorEmail,
			Time:        commit.Time.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return routing.Response{
		Payload: payload,
	}, nil
}

func parseGitCommitsLimit(raw any) (int, error) {
	if raw == nil {
		return defaultGitCommitsLimit, nil
	}

	var parsed int
	switch value := raw.(type) {
	case float64:
		if value != math.Trunc(value) {
			return 0, fmt.Errorf("limit must be integer")
		}
		parsed = int(value)
	case int:
		parsed = value
	case int64:
		parsed = int(value)
	case json.Number:
		number, err := strconv.Atoi(value.String())
		if err != nil {
			return 0, fmt.Errorf("limit must be integer: %w", err)
		}
		parsed = number
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("limit must be integer: %w", err)
		}
		parsed = number
	default:
		return 0, fmt.Errorf("limit must be integer")
	}

	if parsed <= 0 {
		return 0, fmt.Errorf("limit must be > 0")
	}
	if parsed > maxGitCommitsLimit {
		parsed = maxGitCommitsLimit
	}

	return parsed, nil
}

type gitCommitPayload struct {
	// Hash is a full commit hash.
	Hash string `json:"hash"`

	// Message is a commit title/body.
	Message string `json:"message"`

	// Author is a commit author name.
	Author string `json:"author"`

	// AuthorEmail is a commit author email.
	AuthorEmail string `json:"authorEmail,omitempty"`

	// Time is a commit author timestamp in RFC3339 format.
	Time string `json:"time"`
}
