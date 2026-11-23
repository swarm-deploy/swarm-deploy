package git

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	indextrie "github.com/go-git/go-git/v5/utils/merkletrie/index"
	"github.com/go-git/go-git/v5/utils/merkletrie/noder"
)

const maxStatusBytes int64 = 64 << 20

func localStatus(ctx context.Context, path string, limit int) (WorktreeStatus, error) {
	if limit <= 0 {
		return WorktreeStatus{}, errors.New("limit must be positive")
	}
	repo, err := openLocal(ctx, path)
	if err != nil {
		return WorktreeStatus{}, err
	}
	staging, err := repo.Storer.Index()
	if err != nil {
		return WorktreeStatus{}, err
	}
	if len(staging.Entries) > maxLocalEntries {
		return WorktreeStatus{}, ErrScanLimit
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return WorktreeStatus{}, err
	}
	storage, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return WorktreeStatus{}, errors.New("git storage is not on disk")
	}
	root, err := os.OpenRoot(worktree.Filesystem.Root())
	if err != nil {
		return WorktreeStatus{}, err
	}
	defer root.Close()
	git := storage.Filesystem()
	gitRoot, err := os.OpenRoot(git.Root())
	if err != nil {
		return WorktreeStatus{}, err
	}
	defer gitRoot.Close()
	scan := &statusFilesystem{
		Filesystem: worktree.Filesystem, git: git, root: root, gitRoot: gitRoot, ctx: ctx,
	}
	worktree.Filesystem = scan
	repo.Storer = statusStorage{Storer: storage, index: staging}
	status, err := worktree.Status()
	// go-git tolerates ignore-file read errors. Never silently treat a canceled or bounded scan as complete.
	if scan.err != nil {
		return WorktreeStatus{}, scan.err
	}
	if err != nil {
		return WorktreeStatus{}, err
	}
	if err = ctx.Err(); err != nil {
		return WorktreeStatus{}, err
	}
	return summarizeStatus(ctx, repo, staging, status, limit)
}

func summarizeStatus(
	ctx context.Context, repo *gogit.Repository, staging *index.Index, status gogit.Status, limit int,
) (WorktreeStatus, error) {
	result := WorktreeStatus{UntrackedFiles: []string{}}
	paths := make([]string, 0, len(status))
	for name, file := range status {
		if file.Staging == gogit.Untracked && file.Worktree == gogit.Untracked {
			paths = append(paths, name)
		} else if file.Staging != gogit.Unmodified || file.Worktree != gogit.Unmodified {
			result.Dirty = true
		}
	}
	if !result.Dirty && len(paths) > 0 {
		dirty, err := hasStagedChanges(ctx, repo, staging)
		if err != nil {
			return WorktreeStatus{}, err
		}
		result.Dirty = dirty
	}
	sort.Strings(paths)
	if len(paths) > limit {
		result.Truncated = true
		paths = paths[:limit]
	}
	result.UntrackedFiles = append(result.UntrackedFiles, paths...)
	return result, nil
}

// go-git overwrites a staged deletion with "untracked" when the same path
// exists in the worktree. Compare HEAD and the index independently in that case.
func hasStagedChanges(ctx context.Context, repo *gogit.Repository, staging *index.Index) (bool, error) {
	head, err := repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return len(staging.Entries) > 0, nil // unborn branch
	}
	if err != nil {
		return false, err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return false, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return false, err
	}
	changes, err := merkletrie.DiffTreeContext(ctx, object.NewTreeRootNode(tree), indextrie.NewRootNode(staging),
		func(a, b noder.Hasher) bool { return bytes.Equal(a.Hash(), b.Hash()) })
	return len(changes) > 0, err
}

// statusFilesystem bounds go-git's otherwise non-cancelable worktree/ignore traversal.
// It is local to a single status call and does not cache data or share state with pulls.
type statusFilesystem struct {
	billy.Filesystem
	git        billy.Filesystem
	root       *os.Root
	gitRoot    *os.Root
	ctx        context.Context
	operations int
	bytes      int64
	err        error
}

func (s *statusFilesystem) check() error {
	if s.err == nil {
		s.err = s.ctx.Err()
	}
	s.operations++
	if s.err == nil && (s.operations > maxLocalEntries || s.bytes > maxStatusBytes) {
		s.err = ErrScanLimit
	}
	return s.err
}

func (s *statusFilesystem) Open(name string) (billy.File, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	// go-git protects .git in its worktree filesystem. Read only the fixed exclude file through storage.
	gitPath := filepath.ToSlash(name)
	if strings.HasSuffix(gitPath, "/.git/info/exclude") {
		return nil, os.ErrNotExist
	}
	filesystem, root := s.Filesystem, s.root
	if gitPath == ".git/info/exclude" {
		filesystem, root, name = s.git, s.gitRoot, "info/exclude"
	}
	file, err := openStatusFile(filesystem, root, name)
	if err != nil {
		if !os.IsNotExist(err) {
			s.err = err
		}
		return nil, err
	}
	return &statusFile{File: file, scan: s}, nil
}

func (s *statusFilesystem) Lstat(name string) (os.FileInfo, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return s.Filesystem.Lstat(name)
}

func (s *statusFilesystem) ReadDir(path string) ([]os.FileInfo, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	entries, err := s.Filesystem.ReadDir(path)
	if err != nil {
		s.err = err
	}
	s.operations += len(entries)
	if budgetErr := s.check(); budgetErr != nil {
		return nil, budgetErr
	}
	return entries, err
}

func (s *statusFilesystem) Readlink(name string) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	return s.Filesystem.Readlink(name)
}

type statusFile struct {
	*os.File
	scan *statusFilesystem
}

func (*statusFile) Lock() error   { return billy.ErrNotSupported }
func (*statusFile) Unlock() error { return billy.ErrNotSupported }

func (f *statusFile) WriteTo(dst io.Writer) (int64, error) {
	// Keep io.Copy on the budgeted Read path instead of os.File.WriteTo.
	return io.Copy(dst, struct{ io.Reader }{f})
}

func (f *statusFile) Read(buffer []byte) (int, error) {
	if err := f.scan.check(); err != nil {
		return 0, err
	}
	n, err := f.File.Read(buffer)
	f.scan.bytes += int64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		f.scan.err = err
	}
	if budgetErr := f.scan.check(); budgetErr != nil {
		return n, budgetErr
	}
	return n, err
}
