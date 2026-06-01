package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/gitops/git"
)

func (h *handler) GetGitCommit(
	ctx context.Context,
	params generated.GetGitCommitParams,
) (*generated.GitCommitDetailsResponse, error) {
	commitHash := strings.TrimSpace(params.Commit)
	if commitHash == "" {
		return nil, withStatusError(http.StatusBadRequest, errors.New("commit hash is required"))
	}

	commit, err := h.git.Show(ctx, commitHash)
	if err != nil {
		if gitx.IsCommitNotFound(err) {
			return nil, withStatusError(http.StatusNotFound, errors.New("commit not found"))
		}

		return nil, err
	}

	return &generated.GitCommitDetailsResponse{
		FullHash:     commitHash,
		Author:       commit.Author,
		Message:      commit.Message,
		Date:         commit.Time,
		ChangedFiles: commitChangedFiles(commit.Files),
	}, nil
}

func commitChangedFiles(files []gitx.CommitFileDiff) []string {
	if len(files) == 0 {
		return []string{}
	}

	uniqueFiles := make(map[string]struct{}, len(files))
	paths := make([]string, 0, len(files))

	for _, file := range files {
		path := strings.TrimSpace(file.NewPath)
		if path == "" {
			path = strings.TrimSpace(file.OldPath)
		}
		if path == "" {
			continue
		}
		if _, exists := uniqueFiles[path]; exists {
			continue
		}

		uniqueFiles[path] = struct{}{}
		paths = append(paths, path)
	}

	return paths
}
