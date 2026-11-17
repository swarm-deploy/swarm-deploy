package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestLocalStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dirty     bool
		untracked []string
	}{
		{name: "clean", untracked: []string{}},
		{name: "modified", dirty: true, untracked: []string{}},
		{name: "staged", dirty: true, untracked: []string{}},
		{name: "deleted", dirty: true, untracked: []string{}},
		{name: "staged deletion", dirty: true, untracked: []string{}},
		{name: "staged deletion with untracked copy", dirty: true, untracked: []string{"README.md"}},
		{name: "staged addition", dirty: true, untracked: []string{}},
		{name: "untracked", untracked: []string{"new.txt"}},
		{name: "ignored", untracked: []string{}},
		{name: "info exclude", untracked: []string{}},
		{name: "nested ignore", untracked: []string{"dir/keep.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := initSourceRepository(t)
			repo, err := gogit.PlainOpen(dir)
			require.NoError(t, err)
			wt, err := repo.Worktree()
			require.NoError(t, err)
			require.NoError(t, os.Mkdir(filepath.Join(dir, "dir"), 0o700))
			for name, content := range map[string]string{".gitignore": "*.log\nignored/\n", "dir/.gitignore": "*.txt\n!keep.txt\n"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
				_, err = wt.Add(name)
				require.NoError(t, err)
			}
			_, err = wt.Commit("ignore rules", &gogit.CommitOptions{Author: &object.Signature{Name: "test"}})
			require.NoError(t, err)
			switch tc.name {
			case "modified", "staged":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("other"), 0o600))
				if tc.name == "staged" {
					_, err = wt.Add("README.md")
					require.NoError(t, err)
				}
			case "deleted":
				require.NoError(t, os.Remove(filepath.Join(dir, "README.md")))
			case "staged deletion", "staged deletion with untracked copy":
				_, err = wt.Remove("README.md")
				require.NoError(t, err)
				if tc.name == "staged deletion with untracked copy" {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600))
				}
			case "untracked", "staged addition":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o600))
				if tc.name == "staged addition" {
					_, err = wt.Add("new.txt")
					require.NoError(t, err)
				}
			case "ignored":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("ignored"), 0o600))
			case "info exclude":
				require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("private.txt\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "private.txt"), []byte("ignored"), 0o600))
			case "nested ignore":
				for _, name := range []string{"dir/ignored.txt", "dir/keep.txt"} {
					require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
				}
			}
			before := checkoutContents(t, dir)
			repository := NewLazyProxy(config.GitSpec{}, dir)
			status, err := repository.Status(t.Context(), 100)
			require.NoError(t, err)
			assert.Equal(t, tc.dirty, status.Dirty)
			assert.Equal(t, tc.untracked, status.UntrackedFiles)
			assert.False(t, status.Truncated)
			assert.Equal(t, before, checkoutContents(t, dir), "status must not write the index, refs or worktree")
		})
	}
}

func TestLocalStatusSubmodules(t *testing.T) {
	for _, tc := range []struct {
		name string
		head plumbing.Hash
	}{
		{name: "configured without local HEAD"},
		{name: "checked out at a different revision", head: plumbing.NewHash("1111111111111111111111111111111111111111")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, initial := initSourceRepository(t)
			repo, err := gogit.PlainOpen(dir)
			require.NoError(t, err)
			wt, err := repo.Worktree()
			require.NoError(t, err)
			require.NoError(t, os.Mkdir(filepath.Join(dir, "demo"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitmodules"),
				[]byte("[submodule \"demo\"]\n\tpath = demo\n\turl = https://example.com/demo.git\n"), 0o600))
			_, err = wt.Add(".gitmodules")
			require.NoError(t, err)
			index, err := repo.Storer.Index()
			require.NoError(t, err)
			gitlink := index.Add("demo")
			gitlink.Mode = filemode.Submodule
			gitlink.Hash = initial.Hash()
			require.NoError(t, repo.Storer.SetIndex(index))
			_, err = wt.Commit("add submodule", &gogit.CommitOptions{Author: &object.Signature{Name: "test"}})
			require.NoError(t, err)
			submodule, err := wt.Submodule("demo")
			require.NoError(t, err)
			require.NoError(t, submodule.Init())
			if !tc.head.IsZero() {
				module, moduleErr := repo.Storer.Module("demo")
				require.NoError(t, moduleErr)
				require.NoError(t, module.SetReference(plumbing.NewHashReference(plumbing.HEAD, tc.head)))
			}
			before := checkoutContents(t, dir)
			repository := NewLazyProxy(config.GitSpec{}, dir)
			status, err := repository.Status(t.Context(), 100)
			require.NoError(t, err)
			assert.Equal(t, !tc.head.IsZero(), status.Dirty)
			assert.Empty(t, status.UntrackedFiles)
			assert.Equal(t, before, checkoutContents(t, dir))
			if tc.head.IsZero() {
				assert.NoDirExists(t, filepath.Join(dir, ".git", "modules", "demo"))
			}
		})
	}
}

func TestLocalMetricsAndPinnedHistory(t *testing.T) {
	dir, initial := initSourceRepository(t)
	repository := NewLazyProxy(config.GitSpec{}, dir)
	before := checkoutContents(t, dir)
	count, err := repository.CountCommits(t.Context(), initial.Hash().String())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	tracked, err := repository.TrackedFiles(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, tracked)
	size, err := repository.WorkingTreeSize(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(len("hello")), size)
	gitSize, err := repository.GitSize(t.Context())
	require.NoError(t, err)
	var expected int64
	require.NoError(t, filepath.WalkDir(filepath.Join(dir, ".git"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		expected += int64(len(data))
		return err
	}))
	assert.Equal(t, expected, gitSize)
	branch, err := repository.Branch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "master", branch)
	shallow, err := repository.Shallow(t.Context())
	require.NoError(t, err)
	assert.False(t, shallow)
	assert.Equal(t, before, checkoutContents(t, dir))
	repo, err := gogit.PlainOpen(dir)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("next"), 0o600))
	_, err = wt.Add("README.md")
	require.NoError(t, err)
	next, err := wt.Commit("second", &gogit.CommitOptions{Author: &object.Signature{Name: "test"}})
	require.NoError(t, err)
	count, err = repository.CountCommits(t.Context(), initial.Hash().String())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	count, err = repository.CountCommits(t.Context(), next.String())
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	require.NoError(t, repo.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, next)))
	branch, err = repository.Branch(t.Context())
	require.NoError(t, err)
	assert.Empty(t, branch)
	// A real shallow checkout has a boundary commit whose parents are absent.
	require.NoError(t, repo.Storer.SetShallow([]plumbing.Hash{next}))
	require.NoError(t, os.Remove(filepath.Join(dir, ".git", "objects", initial.Hash().String()[:2], initial.Hash().String()[2:])))
	shallow, err = repository.Shallow(t.Context())
	require.NoError(t, err)
	assert.True(t, shallow)
	count, err = repository.CountCommits(t.Context(), next.String())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestLocalLimitsAndCancellation(t *testing.T) {
	dir, _ := initSourceRepository(t)
	repository := NewLazyProxy(config.GitSpec{}, dir)
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("untracked-%d", i)), nil, 0o600))
	}
	status, err := repository.Status(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"untracked-0", "untracked-1"}, status.UntrackedFiles)
	assert.True(t, status.Truncated)
	assert.False(t, status.Dirty)
	status, err = repository.Status(t.Context(), 5)
	require.NoError(t, err)
	assert.False(t, status.Truncated)
	_, err = repository.Status(t.Context(), 0)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = repository.Status(ctx, 2)
	require.ErrorIs(t, err, context.Canceled)
	_, err = repository.GitSize(ctx)
	require.ErrorIs(t, err, context.Canceled)
	_, err = repository.WorkingTreeSize(ctx)
	require.ErrorIs(t, err, context.Canceled)
	_, err = repository.CountCommits(ctx, plumbing.ZeroHash.String())
	require.ErrorIs(t, err, context.Canceled)
	// Large content scans fail explicitly instead of reading an unbounded amount of data.
	file, err := os.Create(filepath.Join(dir, "large"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxStatusBytes+1))
	require.NoError(t, file.Close())
	repo, err := gogit.PlainOpen(dir)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("large")
	require.NoError(t, err)
	file, err = os.OpenFile(filepath.Join(dir, "large"), os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte("modified"), 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, os.Chtimes(filepath.Join(dir, "large"), time.Now().Add(time.Hour), time.Now().Add(time.Hour)))
	_, err = repository.Status(t.Context(), 2)
	require.ErrorIs(t, err, ErrScanLimit)
}

func TestLocalScansDoNotAcquirePullLock(t *testing.T) {
	dir, initial := initSourceRepository(t)
	repo := &GoGitRepository{path: dir, mu: make(chan struct{}, 1)}
	repo.mu <- struct{}{}
	defer repo.release()
	_, err := repo.CountCommits(t.Context(), initial.Hash().String())
	require.NoError(t, err)
	_, err = repo.Status(t.Context(), 10)
	require.NoError(t, err)
}

func checkoutContents(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		result[relative] = sha256.Sum256(data)
		return err
	}))
	return result
}
