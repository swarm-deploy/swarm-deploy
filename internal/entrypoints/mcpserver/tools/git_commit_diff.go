package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/differ"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/git"
)

// GitCommitDiff resolves semantic compose changes for a git commit.
type GitCommitDiff struct {
	repository GitRepository
	differ     CommitDiffer
	stacks     []config.StackSpec
}

// NewGitCommitDiff creates git_commit_diff component.
func NewGitCommitDiff(repository GitRepository, stacks []config.StackSpec, composeDiffer CommitDiffer) *GitCommitDiff {
	copiedStacks := make([]config.StackSpec, len(stacks))
	copy(copiedStacks, stacks)

	return &GitCommitDiff{
		repository: repository,
		differ:     composeDiffer,
		stacks:     copiedStacks,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GitCommitDiff) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "git_commit_diff",
		Description: "Returns semantic changes by stack/service for a specific git commit based on compose file differences.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"commit",
			},
			"properties": map[string]any{
				"commit": map[string]any{
					"type":        "string",
					"description": "Commit hash to inspect.",
				},
			},
		},
	}
}

// Execute runs git_commit_diff tool.
func (g *GitCommitDiff) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	commitHash, err := parseStringParam(request.Payload["commit"], "commit")
	if err != nil {
		return routing.Response{}, err
	}
	if commitHash == "" {
		return routing.Response{}, fmt.Errorf("commit is required")
	}

	commit, err := g.repository.Show(ctx, commitHash)
	if err != nil {
		return routing.Response{}, err
	}

	composeFiles := g.collectComposeFiles(commit.Files)
	diff, err := g.differ.Compare(composeFiles)
	if err != nil {
		return routing.Response{}, err
	}
	diff = sanitizeDiffForModel(diff)

	payload := struct {
		// Commit is an inspected commit hash.
		Commit string `json:"commit"`

		// Author is a commit author name.
		Author string `json:"author"`

		// AuthorEmail is a commit author email.
		AuthorEmail string `json:"authorEmail,omitempty"`

		// Time is a commit author timestamp.
		Time string `json:"time"`

		// Diff contains semantic per-service changes.
		Diff differ.Diff `json:"diff"`
	}{
		Commit:      commitHash,
		Author:      commit.Author,
		AuthorEmail: commit.AuthorEmail,
		Time:        commit.Time.UTC().Format("2006-01-02T15:04:05Z07:00"),
		Diff:        diff,
	}

	return routing.Response{Payload: payload}, nil
}

func sanitizeDiffForModel(diff differ.Diff) differ.Diff {
	for serviceIndex := range diff.Services {
		for environmentIndex := range diff.Services[serviceIndex].Environment {
			diff.Services[serviceIndex].Environment[environmentIndex].Value = ""
		}
	}

	return diff
}

func (g *GitCommitDiff) collectComposeFiles(fileDiffs []git.CommitFileDiff) []differ.ComposeFile {
	stacksByComposePath := map[string]string{}
	for _, stack := range g.stacks {
		composePath := normalizeComposePath(stack.ComposeFile)
		if composePath == "" {
			continue
		}
		stacksByComposePath[composePath] = stack.Name
	}

	composeFiles := make([]differ.ComposeFile, 0, len(fileDiffs))
	for _, fileDiff := range fileDiffs {
		oldPath := normalizeComposePath(fileDiff.OldPath)
		newPath := normalizeComposePath(fileDiff.NewPath)

		stackName, composePath := resolveStackByComposePath(stacksByComposePath, oldPath, newPath)
		if stackName == "" {
			continue
		}

		composeFiles = append(composeFiles, differ.ComposeFile{
			StackName:      stackName,
			ComposePath:    composePath,
			OldComposeFile: fileDiff.OldContent,
			NewComposeFile: fileDiff.NewContent,
		})
	}

	sort.Slice(composeFiles, func(i, j int) bool {
		if composeFiles[i].StackName == composeFiles[j].StackName {
			return composeFiles[i].ComposePath < composeFiles[j].ComposePath
		}
		return composeFiles[i].StackName < composeFiles[j].StackName
	})

	return composeFiles
}

func normalizeComposePath(path string) string {
	return strings.TrimPrefix(strings.TrimSpace(path), "./")
}

func resolveStackByComposePath(stacksByComposePath map[string]string, oldPath string, newPath string) (string, string) {
	if stackName, exists := stacksByComposePath[newPath]; exists {
		return stackName, newPath
	}
	if stackName, exists := stacksByComposePath[oldPath]; exists {
		return stackName, oldPath
	}
	return "", ""
}
