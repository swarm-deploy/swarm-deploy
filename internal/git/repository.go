package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	gogit "github.com/go-git/go-git/v5"
	gogitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type GoGitRepository struct {
	path       string
	pullBranch string
	auth       transport.AuthMethod

	repository    *gogit.Repository
	repositoryURL string
}

func NewRepository(spec config.GitRepositorySpec, path string) Repository {
	return NewLazyProxy(spec, path)
}

func NewGoGitRepository(
	ctx context.Context,
	cfg config.GitRepositorySpec,
	path string,
) (*GoGitRepository, error) {
	authMethod, err := resolveAuthMethod(cfg.Auth)
	if err != nil {
		return nil, err
	}

	return newGoGitRepository(ctx, path, cfg.Branch, authMethod, cfg.Repository)
}

func newGoGitRepository(
	ctx context.Context,
	path,
	branch string,
	auth transport.AuthMethod,
	repoURL string,
) (*GoGitRepository, error) {
	repo, err := openRepository(ctx, path, repoURL, branch, auth)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}

	return &GoGitRepository{
		path:          path,
		pullBranch:    branch,
		auth:          auth,
		repository:    repo,
		repositoryURL: repoURL,
	}, nil
}

func (r *GoGitRepository) Pull(ctx context.Context) error {
	worktree, err := r.repository.Worktree()
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}

	err = worktree.PullContext(ctx, &gogit.PullOptions{
		RemoteName:    "origin",
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(r.pullBranch),
		Auth:          r.auth,
		Force:         true,
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("git pull: %w", err)
	}

	return nil
}

func (r *GoGitRepository) Head(context.Context) (string, error) {
	headRef, err := r.repository.Head()
	if err != nil {
		return "", err
	}

	return headRef.Hash().String(), nil
}

func (r *GoGitRepository) List(ctx context.Context, limit int) ([]CommitMeta, error) {
	if limit <= 0 {
		return nil, errors.New("limit must be > 0")
	}

	headRef, err := r.repository.Head()
	if err != nil {
		return nil, fmt.Errorf("resolve head: %w", err)
	}

	commitIterator, err := r.repository.Log(&gogit.LogOptions{
		From: headRef.Hash(),
	})
	if err != nil {
		return nil, fmt.Errorf("read commit log: %w", err)
	}
	defer commitIterator.Close()

	commits := make([]CommitMeta, 0, limit)
	for len(commits) < limit {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		commit, nextErr := commitIterator.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("iterate commit log: %w", nextErr)
		}

		commits = append(commits, CommitMeta{
			Hash:        commit.Hash.String(),
			Message:     strings.TrimSpace(commit.Message),
			Author:      commit.Author.Name,
			AuthorEmail: commit.Author.Email,
			Time:        commit.Author.When,
		})
	}

	return commits, nil
}

func (r *GoGitRepository) Show(ctx context.Context, commitHash string) (Commit, error) {
	trimmedHash := strings.TrimSpace(commitHash)
	if trimmedHash == "" {
		return Commit{}, errors.New("commit hash is required")
	}

	commit, err := r.repository.CommitObject(plumbing.NewHash(trimmedHash))
	if err != nil {
		return Commit{}, fmt.Errorf("find commit %q: %w", trimmedHash, err)
	}

	fileDiffs, err := buildCommitFileDiffs(ctx, commit)
	if err != nil {
		return Commit{}, fmt.Errorf("build commit %q file diff: %w", trimmedHash, err)
	}

	return Commit{
		Author:      commit.Author.Name,
		AuthorEmail: commit.Author.Email,
		Time:        commit.Author.When,
		Files:       fileDiffs,
	}, nil
}

func (r *GoGitRepository) Branch(ctx context.Context, branchName string) (Repository, error) {
	newPath := r.path + "-" + branchName

	repo, err := newGoGitRepository(ctx, newPath, r.pullBranch, r.auth, r.repositoryURL)
	if err != nil {
		return nil, err
	}

	worktree, err := repo.repository.Worktree()
	if err != nil {
		return nil, fmt.Errorf("open worktree: %w", err)
	}

	err = r.checkoutBranch(worktree, branchName, true)
	if err != nil {
		return nil, fmt.Errorf("create branch %q: %w", branchName, err)
	}

	return repo, nil
}

func (r *GoGitRepository) Add(_ context.Context, path string) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return errors.New("path is required")
	}

	worktree, err := r.repository.Worktree()
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}

	_, err = worktree.Add(trimmedPath)
	if err != nil {
		return fmt.Errorf("git add %q: %w", trimmedPath, err)
	}

	return nil
}

func (r *GoGitRepository) Commit(_ context.Context, message string, author CommitAuthor) (string, error) {
	commitMessage := strings.TrimSpace(message)
	if commitMessage == "" {
		return "", errors.New("commit message is required")
	}

	worktree, err := r.repository.Worktree()
	if err != nil {
		return "", fmt.Errorf("open worktree: %w", err)
	}

	commitHash, err := worktree.Commit(commitMessage, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  strings.TrimSpace(author.Name),
			Email: strings.TrimSpace(author.Email),
			When:  time.Now().UTC(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}

	return commitHash.String(), nil
}

func (r *GoGitRepository) Push(ctx context.Context, branch string) error {
	err := r.repository.PushContext(ctx, &gogit.PushOptions{
		RemoteName: "origin",
		Auth:       r.auth,
		RefSpecs: []gogitcfg.RefSpec{
			gogitcfg.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", branch, branch)),
		},
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("git push branch %q: %w", branch, err)
	}

	return nil
}

func (r *GoGitRepository) WorkingDir() string {
	return r.path
}

func (r *GoGitRepository) checkoutBranch(worktree *gogit.Worktree, branch string, create bool) error {
	return worktree.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Create: create,
		Force:  true,
	})
}

func buildCommitFileDiffs(ctx context.Context, commit *object.Commit) ([]CommitFileDiff, error) {
	commitTree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("resolve commit tree: %w", err)
	}

	var parentTree *object.Tree
	if commit.NumParents() > 0 {
		parentCommit, parentCommitErr := commit.Parent(0)
		if parentCommitErr != nil {
			return nil, fmt.Errorf("resolve parent commit: %w", parentCommitErr)
		}

		parentTree, err = parentCommit.Tree()
		if err != nil {
			return nil, fmt.Errorf("resolve parent tree: %w", err)
		}
	}

	changes, err := object.DiffTreeWithOptions(ctx, parentTree, commitTree, object.DefaultDiffTreeOptions)
	if err != nil {
		return nil, fmt.Errorf("diff trees: %w", err)
	}

	sort.Slice(changes, func(i, j int) bool {
		return changedPath(changes[i]) < changedPath(changes[j])
	})

	diffs := make([]CommitFileDiff, 0, len(changes))
	for _, change := range changes {
		fileDiff, fileDiffErr := buildCommitFileDiff(ctx, change)
		if fileDiffErr != nil {
			return nil, fileDiffErr
		}
		diffs = append(diffs, fileDiff)
	}

	return diffs, nil
}

func changedPath(change *object.Change) string {
	if change.To.Name != "" {
		return change.To.Name
	}
	return change.From.Name
}

func buildCommitFileDiff(ctx context.Context, change *object.Change) (CommitFileDiff, error) {
	fromFile, toFile, err := change.Files()
	if err != nil {
		return CommitFileDiff{}, fmt.Errorf("read changed files: %w", err)
	}

	oldContent, err := readTextFileContent(fromFile)
	if err != nil {
		return CommitFileDiff{}, fmt.Errorf("read old file content: %w", err)
	}

	newContent, err := readTextFileContent(toFile)
	if err != nil {
		return CommitFileDiff{}, fmt.Errorf("read new file content: %w", err)
	}

	patch, err := change.PatchContext(ctx)
	if err != nil {
		return CommitFileDiff{}, fmt.Errorf("build file patch: %w", err)
	}

	return CommitFileDiff{
		OldPath:    strings.TrimSpace(change.From.Name),
		NewPath:    strings.TrimSpace(change.To.Name),
		OldContent: oldContent,
		NewContent: newContent,
		Patch:      patch.String(),
	}, nil
}

func readTextFileContent(file *object.File) (string, error) {
	if file == nil {
		return "", nil
	}

	isBinary, err := file.IsBinary()
	if err != nil {
		return "", err
	}
	if isBinary {
		return "", nil
	}

	content, err := file.Contents()
	if err != nil {
		return "", err
	}

	return content, nil
}

func openRepository(
	ctx context.Context,
	path string,
	url string,
	branch string,
	auth transport.AuthMethod,
) (*gogit.Repository, error) {
	repo, err := gogit.PlainOpen(path)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, gogit.ErrRepositoryNotExists) {
		return nil, err
	}

	if err = os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("create repository dir: %w", err)
	}

	slog.InfoContext(ctx, "[git] cloning repository", slog.String("repository.url", url))

	repo, err = gogit.PlainCloneContext(ctx, path, false, &gogit.CloneOptions{
		URL:           url,
		Auth:          auth,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
	})
	if err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}

	return repo, nil
}

func resolveAuthMethod(auth config.GitAuthSpec) (transport.AuthMethod, error) {
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "none":
		//nolint:nilnil // nil auth method explicitly means anonymous access for go-git.
		return nil, nil
	case "http":
		password := auth.HTTP.ResolvePassword()
		username := auth.HTTP.ResolveUsername()
		if username == "" || password == "" {
			return nil, errors.New("http auth requires non-empty username and password/token")
		}
		return &githttp.BasicAuth{
			Username: username,
			Password: password,
		}, nil
	case "ssh":
		return buildSSHAuthMethod(auth.SSH)
	default:
		return nil, fmt.Errorf("unsupported git auth type: %s", auth.Type)
	}
}

func buildSSHAuthMethod(auth config.GitSSHAuthSpec) (transport.AuthMethod, error) {
	user := auth.User
	if user == "" {
		user = "git"
	}

	var (
		pk     *gitssh.PublicKeys
		keyErr error
	)

	if auth.PrivateKeyPath != "" {
		pk, keyErr = gitssh.NewPublicKeysFromFile(user, auth.PrivateKeyPath, string(auth.Passphrase.Content))
		if keyErr != nil {
			return nil, fmt.Errorf("read ssh private key from file: %w", keyErr)
		}
	} else {
		return nil, errors.New("ssh auth requires privateKeyPath")
	}

	if auth.InsecureIgnoreHostKey {
		pk.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
			//nolint:gosec // This mode is explicitly requested by configuration for legacy/private infrastructures.
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
		return pk, nil
	}

	if auth.KnownHostsPath != "" {
		callback, callbackErr := knownhosts.New(auth.KnownHostsPath)
		if callbackErr != nil {
			return nil, fmt.Errorf("build known_hosts callback: %w", callbackErr)
		}
		pk.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
			HostKeyCallback: callback,
		}
	}

	return pk, nil
}
