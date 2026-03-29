package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

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

type Repository interface {
	// Pull fetches latest changes from origin for configured branch.
	Pull(ctx context.Context) error
	// Head resolves current HEAD revision hash.
	Head(ctx context.Context) (string, error)
	// List returns latest commits from HEAD up to the provided limit.
	List(ctx context.Context, limit int) ([]CommitMeta, error)
	// Show returns commit metadata and per-file diff for a given revision.
	Show(ctx context.Context, commitHash string) (Commit, error)
}

type GoGitRepository struct {
	branch string
	auth   transport.AuthMethod

	repository *gogit.Repository
}

func NewRepository(spec config.GitSpec, path string) Repository {
	return NewLazyProxy(spec, path)
}

func NewGoGitRepository(ctx context.Context, spec config.GitSpec, path string) (*GoGitRepository, error) {
	authMethod, err := resolveAuthMethod(spec.Pull.Auth)
	if err != nil {
		return nil, err
	}

	repo, err := openRepository(ctx, path, spec.Pull.Repository, spec.Pull.Branch, authMethod)
	if err != nil {
		return nil, err
	}

	return &GoGitRepository{
		branch:     spec.Pull.Branch,
		auth:       authMethod,
		repository: repo,
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
		ReferenceName: plumbing.NewBranchReferenceName(r.branch),
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
