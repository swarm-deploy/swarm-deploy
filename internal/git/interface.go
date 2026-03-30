package git

import (
	"context"
	"time"
)

type Repository interface {
	// WorkingDir returns local repository working directory path.
	WorkingDir() string

	// Pull fetches latest changes from origin for configured branch.
	Pull(ctx context.Context) error
	// Head resolves current HEAD revision hash.
	Head(ctx context.Context) (string, error)
	// List returns latest commits from HEAD up to the provided limit.
	List(ctx context.Context, limit int) ([]CommitMeta, error)
	// Show returns commit metadata and per-file diff for a given revision.
	Show(ctx context.Context, commitHash string) (Commit, error)

	// Branch create isolated branch and repository.
	Branch(ctx context.Context, branchName string) (Repository, error)

	// Add stages a file path relative to repository root.
	Add(ctx context.Context, path string) error
	// Commit creates a commit from staged changes and returns commit hash.
	Commit(ctx context.Context, message string, author CommitAuthor) (string, error)
	// Push pushes branch to origin.
	Push(ctx context.Context, branch string) error
}

// Commit describes git commit metadata and per-file diff data.
type Commit struct {
	// Author is a commit author name.
	Author string
	// AuthorEmail is a commit author email.
	AuthorEmail string
	// Time is a commit author timestamp.
	Time time.Time
	// Files contains per-file diffs between commit parent and commit itself.
	Files []CommitFileDiff
}

// CommitMeta describes lightweight git commit metadata.
type CommitMeta struct {
	// Hash is a full commit hash.
	Hash string
	// Message is a commit message title/body.
	Message string
	// Author is a commit author name.
	Author string
	// AuthorEmail is a commit author email.
	AuthorEmail string
	// Time is a commit author timestamp.
	Time time.Time
}

// CommitFileDiff contains one changed file snapshot in commit diff.
type CommitFileDiff struct {
	// OldPath is a file path before change.
	OldPath string
	// NewPath is a file path after change.
	NewPath string
	// OldContent is a text file content before change. Empty for binary/non-existent files.
	OldContent string
	// NewContent is a text file content after change. Empty for binary/non-existent files.
	NewContent string
	// Patch is a unified diff for this file.
	Patch string
}

// CommitAuthor describes commit author identity.
type CommitAuthor struct {
	// Name is a commit author display name.
	Name string
	// Email is a commit author email.
	Email string
}
