package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	gitclient "github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/object"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type authMethod struct {
	http *githttp.BasicAuth
	ssh  *gitssh.PublicKeys
}

func (a *authMethod) clientOptions() []gitclient.Option {
	if a == nil {
		return nil
	}

	if a.http != nil {
		return []gitclient.Option{gitclient.WithHTTPAuth(a.http)}
	}

	if a.ssh != nil {
		return []gitclient.Option{gitclient.WithSSHAuth(a.ssh)}
	}

	return nil
}

type GoGitRepository struct {
	path string

	branch string
	auth   *authMethod

	repository *gogit.Repository
}

func NewRepository(spec config.GitSpec, path string) Repository {
	return NewLazyProxy(spec, path)
}

func NewGoGitRepository(ctx context.Context, spec config.GitSpec, path string) (*GoGitRepository, error) {
	authMethod, err := resolveAuthMethod(spec.Auth)
	if err != nil {
		return nil, err
	}

	repo, err := openRepository(ctx, path, spec.Repository, spec.Branch, authMethod)
	if err != nil {
		return nil, err
	}

	return &GoGitRepository{
		path:       path,
		branch:     spec.Branch,
		auth:       authMethod,
		repository: repo,
	}, nil
}

func (r *GoGitRepository) WorkingDir() string {
	return r.path
}

func (r *GoGitRepository) ReadFile(_ context.Context, path string) ([]byte, error) {
	fullPath := filepath.Join(r.path, path)
	return os.ReadFile(fullPath)
}

func (r *GoGitRepository) Pull(ctx context.Context) (PullResult, error) {
	worktree, err := r.repository.Worktree()
	if err != nil {
		return PullResult{}, fmt.Errorf("open worktree: %w", err)
	}

	previousHash, err := r.Head(ctx)
	if err != nil {
		return PullResult{}, fmt.Errorf("get current head: %w", err)
	}

	err = worktree.PullContext(ctx, &gogit.PullOptions{
		RemoteName:    "origin",
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(r.branch),
		ClientOptions: r.auth.clientOptions(),
		Force:         true,
	})
	if err != nil && !errors.Is(err, gogit.NoErrAlreadyUpToDate) {
		return PullResult{}, fmt.Errorf("git pull: %w", err)
	}

	newHash, err := r.Head(ctx)
	if err != nil {
		return PullResult{}, fmt.Errorf("get current head after pull: %w", err)
	}

	return PullResult{
		OldRevision: previousHash,
		NewRevision: newHash,
		Updated:     previousHash != newHash,
	}, nil
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

func (r *GoGitRepository) Diff(ctx context.Context, oldRevision string, newRevision string) ([]CommitFileDiff, error) {
	if oldRevision == "" {
		return nil, errors.New("old revision is required")
	}

	if newRevision == "" {
		return nil, errors.New("new revision is required")
	}

	if oldRevision == newRevision {
		return nil, nil
	}

	oldCommit, err := r.repository.CommitObject(plumbing.NewHash(oldRevision))
	if err != nil {
		return nil, fmt.Errorf("find old revision %q: %w", oldRevision, err)
	}

	newCommit, err := r.repository.CommitObject(plumbing.NewHash(newRevision))
	if err != nil {
		return nil, fmt.Errorf("find new revision %q: %w", newRevision, err)
	}

	oldTree, err := oldCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("resolve old revision tree: %w", err)
	}

	newTree, err := newCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("resolve new revision tree: %w", err)
	}

	fileDiffs, err := buildFileDiffsBetweenTrees(ctx, oldTree, newTree)
	if err != nil {
		return nil, fmt.Errorf("build diff between revisions %q..%q: %w", oldRevision, newRevision, err)
	}

	return fileDiffs, nil
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
		Message:     strings.TrimSpace(commit.Message),
		Time:        commit.Author.When,
		Files:       fileDiffs,
	}, nil
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

	return buildFileDiffsBetweenTrees(ctx, parentTree, commitTree)
}

func buildFileDiffsBetweenTrees(
	ctx context.Context,
	oldTree *object.Tree,
	newTree *object.Tree,
) ([]CommitFileDiff, error) {
	changes, err := object.DiffTreeWithOptions(ctx, oldTree, newTree, object.DefaultDiffTreeOptions)
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
	auth *authMethod,
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

	repo, err = gogit.PlainCloneContext(ctx, path, &gogit.CloneOptions{
		URL:           url,
		ClientOptions: auth.clientOptions(),
		Bare:          false,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
	})
	if err != nil {
		return nil, fmt.Errorf("git clone: %w", err)
	}

	return repo, nil
}

func resolveAuthMethod(auth config.GitAuthSpec) (*authMethod, error) {
	switch auth.Type {
	case "", config.GitAuthTypeNone:
		//nolint:nilnil // nil auth method explicitly means anonymous access for go-git.
		return nil, nil
	case config.GitAuthTypeHTTP:
		password := auth.HTTP.ResolvePassword()
		username := auth.HTTP.ResolveUsername()
		if username == "" || password == "" {
			return nil, errors.New("http auth requires username+passwordPath or tokenPath")
		}
		return &authMethod{
			http: &githttp.BasicAuth{
				Username: username,
				Password: password,
			},
		}, nil
	case config.GitAuthTypeSSH:
		return buildSSHAuthMethod(auth.SSH)
	default:
		return nil, fmt.Errorf("unsupported git auth type: %s", auth.Type)
	}
}

func buildSSHAuthMethod(auth config.GitSSHAuthSpec) (*authMethod, error) {
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
		return &authMethod{ssh: pk}, nil
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

	return &authMethod{ssh: pk}, nil
}
