package git

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

const maxLocalEntries = 100000

// ErrScanLimit means a local traversal exceeded its work budget; no exact metric is available.
var ErrScanLimit = errors.New("local Git scan limit exceeded")

// WorktreeStatus describes one read-only status operation. Untracked files do not make tracked files dirty.
type WorktreeStatus struct {
	// Dirty indicates staged or unstaged changes to tracked files.
	Dirty bool
	// UntrackedFiles lists repository-relative paths, excluding ignored files.
	UntrackedFiles []string
	// Truncated indicates that more untracked paths exist than were returned.
	Truncated bool
}

// openLocal creates an independent go-git handle over the existing checkout. It never clones or fetches.
// Separate handles avoid races on go-git's mutable object indexes without holding the pull lock during scans.
func openLocal(ctx context.Context, path string) (*gogit.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return gogit.PlainOpen(path)
}

func localBranch(ctx context.Context, path string) (string, error) {
	repo, err := openLocal(ctx, path)
	if err != nil {
		return "", err
	}
	ref, err := repo.Reference(plumbing.HEAD, false)
	if err != nil {
		return "", err
	}
	if ref.Type() == plumbing.SymbolicReference && ref.Target().IsBranch() {
		return ref.Target().Short(), nil
	}
	return "", nil
}

func localShallow(ctx context.Context, path string) (bool, error) {
	repo, err := openLocal(ctx, path)
	if err != nil {
		return false, err
	}
	shallow, err := repo.Storer.Shallow()
	return len(shallow) > 0, err
}

func localCountCommits(ctx context.Context, path, revision string) (int, error) {
	repo, err := openLocal(ctx, path)
	if err != nil {
		return 0, err
	}
	if !plumbing.IsHash(revision) {
		return 0, errors.New("revision must be a full Git commit hash")
	}
	shallow, err := repo.Storer.Shallow()
	if err != nil {
		return 0, err
	}
	boundaries := make(map[plumbing.Hash]bool, len(shallow))
	for _, hash := range shallow {
		boundaries[hash] = true
	}
	seen := make(map[plumbing.Hash]bool)
	pending := []plumbing.Hash{plumbing.NewHash(revision)}
	for len(pending) > 0 {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		hash := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[hash] {
			continue
		}
		if len(seen) >= maxLocalEntries {
			return 0, ErrScanLimit
		}
		commit, commitErr := repo.CommitObject(hash)
		if commitErr != nil {
			return 0, commitErr
		}
		seen[hash] = true
		if !boundaries[hash] {
			pending = append(pending, commit.ParentHashes...)
		}
	}
	return len(seen), nil
}

func localGitSize(ctx context.Context, path string) (int64, error) {
	repo, err := openLocal(ctx, path)
	if err != nil {
		return 0, err
	}
	storage, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return 0, errors.New("git storage is not on disk")
	}
	return directorySize(ctx, storage.Filesystem().Root(), false)
}

func localWorkingTreeSize(ctx context.Context, path string) (int64, error) {
	return directorySize(ctx, path, true)
}

func directorySize(ctx context.Context, root string, excludeGit bool) (int64, error) {
	var size int64
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > maxLocalEntries {
			return ErrScanLimit
		}
		if excludeGit && path == filepath.Join(root, ".git") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return size, nil
}

func localTrackedFiles(ctx context.Context, path string) (int, error) {
	repo, err := openLocal(ctx, path)
	if err != nil {
		return 0, err
	}
	index, err := repo.Storer.Index()
	if err != nil {
		return 0, err
	}
	if len(index.Entries) > maxLocalEntries {
		return 0, ErrScanLimit
	}
	names := make(map[string]struct{}, len(index.Entries))
	for _, entry := range index.Entries {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		names[entry.Name] = struct{}{}
	}
	return len(names), nil
}

// Branch returns the current branch, or an empty string for detached HEAD.
func (r *GoGitRepository) Branch(ctx context.Context) (string, error) {
	return localBranch(ctx, r.path)
}

// Shallow reports whether history has shallow boundaries.
func (r *GoGitRepository) Shallow(ctx context.Context) (bool, error) {
	return localShallow(ctx, r.path)
}

// CountCommits counts reachable commits at the given revision, stopping at shallow boundaries.
func (r *GoGitRepository) CountCommits(ctx context.Context, revision string) (int, error) {
	return localCountCommits(ctx, r.path, revision)
}

// GitSize sums file sizes in the local Git storage, including packfiles.
func (r *GoGitRepository) GitSize(ctx context.Context) (int64, error) {
	return localGitSize(ctx, r.path)
}

// WorkingTreeSize sums working tree file sizes, excluding .git and without following symlinks.
func (r *GoGitRepository) WorkingTreeSize(ctx context.Context) (int64, error) {
	return localWorkingTreeSize(ctx, r.path)
}

// TrackedFiles counts distinct paths in the current Git index.
func (r *GoGitRepository) TrackedFiles(ctx context.Context) (int, error) {
	return localTrackedFiles(ctx, r.path)
}

// Status reads tracked changes and a bounded list of untracked files, respecting ignore rules.
func (r *GoGitRepository) Status(ctx context.Context, limit int) (WorktreeStatus, error) {
	return localStatus(ctx, r.path, limit)
}
