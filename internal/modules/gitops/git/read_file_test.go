package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoGitRepositoryReadFile(t *testing.T) {
	dir, initial := initSourceRepository(t)
	repository, err := gogit.PlainOpen(dir)
	require.NoError(t, err)
	worktree, err := repository.Worktree()
	require.NoError(t, err)
	repo := &GoGitRepository{path: dir, repository: repository, mu: make(chan struct{}, 1)}

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "deep", "nested"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deep", "value"), []byte("deep"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deep", "nested", "keep"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("next"), 0o600))
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))

	for name, target := range map[string]string{
		"file-link":       "README.md",
		"dir-link":        "deep/nested",
		"abs-link":        outside,
		"relative-escape": "../outside",
		"cycle-a":         "cycle-b",
		"cycle-b":         "cycle-a",
		"dangling":        "missing",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(dir, name)))
	}
	_, err = worktree.Add(".")
	require.NoError(t, err)
	next, err := worktree.Commit("links", &gogit.CommitOptions{Author: &object.Signature{Name: "test"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("uncommitted"), 0o600))

	for _, tt := range []struct {
		path, revision, want string
	}{
		{"README.md", initial.Hash().String(), "hello"},
		{"README.md", next.String(), "next"},
		{"file-link", next.String(), "next"},
		{"dir-link/keep", next.String(), ""},
		{"dir-link/../value", next.String(), "deep"},
	} {
		t.Run(tt.path+"/"+tt.want, func(t *testing.T) {
			data, readErr := repo.ReadFile(t.Context(), tt.revision, tt.path)
			require.NoError(t, readErr)
			require.Equal(t, tt.want, string(data))
		})
	}

	for _, name := range []string{outside, "../outside", "abs-link", "abs-link/../README.md",
		"relative-escape", "relative-escape/../README.md", "cycle-a", "dangling", "deep", "dir-link", ""} {
		t.Run("reject "+name, func(t *testing.T) {
			data, readErr := repo.ReadFile(t.Context(), next.String(), name)
			require.Error(t, readErr)
			require.Nil(t, data)
		})
	}

	for _, revision := range []string{"", "HEAD", "invalid", "0000000000000000000000000000000000000000"} {
		_, err = repo.ReadFile(t.Context(), revision, "README.md")
		require.Error(t, err)
	}
}

// A canceled reconciliation must not wait for a slow Git pull to release the repository lock.
func TestGoGitRepositoryReadFileCancelsWhileLocked(t *testing.T) {
	repo := &GoGitRepository{mu: make(chan struct{}, 1)}
	repo.mu <- struct{}{}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := repo.ReadFile(ctx, "revision", "README.md")
	require.ErrorIs(t, err, context.Canceled)
}

func TestGoGitRepositoryPullWhileLocked(t *testing.T) {
	tests := []struct {
		name               string
		cancel             bool
		expectedCategory   FailureCategory
		expectedDiagnostic string
	}{
		{
			name:               "pull timeout reports lock contention",
			expectedCategory:   FailureOther,
			expectedDiagnostic: repositoryLockTimeoutDiagnostic,
		},
		{
			name:               "canceled caller keeps the pull failure classification",
			cancel:             true,
			expectedCategory:   FailureNetwork,
			expectedDiagnostic: "Git repository is temporarily unreachable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &GoGitRepository{mu: make(chan struct{}, 1), pullTimeout: 10 * time.Millisecond}
			repo.mu <- struct{}{}
			ctx := t.Context()
			if tt.cancel {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}

			_, err := repo.Pull(ctx)

			var pullErr *PullError
			require.ErrorAs(t, err, &pullErr)
			category, diagnostic := PullFailure(err)
			assert.Equal(t, tt.expectedCategory, category)
			assert.Equal(t, tt.expectedDiagnostic, diagnostic)
		})
	}
}
