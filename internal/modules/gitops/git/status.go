package git

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"
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
	index, err := repo.Storer.Index()
	if err != nil {
		return WorktreeStatus{}, err
	}
	if len(index.Entries) > maxLocalEntries {
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
	scan := &statusFilesystem{Filesystem: worktree.Filesystem, git: storage.Filesystem(), ctx: ctx}
	worktree.Filesystem = scan
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
	result := WorktreeStatus{UntrackedFiles: []string{}}
	paths := make([]string, 0, len(status))
	for name, file := range status {
		if file.Staging == gogit.Untracked && file.Worktree == gogit.Untracked {
			paths = append(paths, name)
		} else if file.Staging != gogit.Unmodified || file.Worktree != gogit.Unmodified {
			result.Dirty = true
		}
	}
	sort.Strings(paths)
	if len(paths) > limit {
		result.Truncated = true
		paths = paths[:limit]
	}
	result.UntrackedFiles = append(result.UntrackedFiles, paths...)
	return result, nil
}

// statusFilesystem bounds go-git's otherwise non-cancelable worktree/ignore traversal.
// It is local to a single status call and does not cache data or share state with pulls.
type statusFilesystem struct {
	billy.Filesystem
	git        billy.Filesystem
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
	if strings.HasSuffix(name, "/.git/info/exclude") {
		return nil, os.ErrNotExist
	}
	filesystem := s.Filesystem
	if name == ".git/info/exclude" {
		filesystem, name = s.git, "info/exclude"
	}
	file, err := filesystem.Open(name)
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
	info, err := s.Filesystem.Lstat(name)
	if err == nil && !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return nil, errors.New("unsupported special file in working tree")
	}
	return info, err
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
	billy.File
	scan *statusFilesystem
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
