//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=$GOFILE -destination=mock.go -package=git
package git

import (
	"context"
	"time"
)

type Repository interface {
	// Branch returns the current branch, or an empty string for detached HEAD.
	Branch(ctx context.Context) (string, error)
	// Shallow reports whether history has shallow boundaries.
	Shallow(ctx context.Context) (bool, error)
	// CountCommits counts reachable commits at the given revision, stopping at shallow boundaries.
	CountCommits(ctx context.Context, revision string) (int, error)
	// GitSize sums file sizes in the local Git storage, including packfiles.
	GitSize(ctx context.Context) (int64, error)
	// WorkingTreeSize sums working tree file sizes, excluding .git and without following symlinks.
	WorkingTreeSize(ctx context.Context) (int64, error)
	// TrackedFiles counts distinct paths in the current Git index.
	TrackedFiles(ctx context.Context) (int, error)
	// Status reads tracked changes and a bounded list of untracked files, respecting ignore rules.
	Status(ctx context.Context, limit int) (WorktreeStatus, error)

	// WorkingDir returns local repository path.
	WorkingDir() string

	// ReadFile reads a repository-relative file from the specified Git commit hash.
	ReadFile(ctx context.Context, revision, path string) ([]byte, error)

	// Pull fetches latest changes from origin for configured branch.
	Pull(ctx context.Context) (PullResult, error)
	// Head resolves current HEAD revision hash.
	Head(ctx context.Context) (string, error)
	// List returns latest commits from HEAD up to the provided limit.
	List(ctx context.Context, limit int) ([]CommitMeta, error)
	// Diff returns per-file diff data between two revisions.
	Diff(ctx context.Context, oldRevision string, newRevision string) ([]CommitFileDiff, error)
	// Show returns commit metadata and per-file diff for a given revision.
	Show(ctx context.Context, commitHash string) (Commit, error)
}

// Commit describes git commit metadata and per-file diff data.
type Commit struct {
	// Author is a commit author name.
	Author string
	// AuthorEmail is a commit author email.
	AuthorEmail string
	// Message is a commit message title/body.
	Message string
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

type PullResult struct {
	OldRevision string
	NewRevision string
	Updated     bool
}
