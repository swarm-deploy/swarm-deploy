package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/config"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Repository interface {
	// Pull fetches latest changes from origin for configured branch.
	Pull(ctx context.Context) error
	// Head resolves current HEAD revision hash.
	Head(ctx context.Context) (string, error)
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
	authMethod, err := resolveAuthMethod(spec.Auth)
	if err != nil {
		return nil, err
	}

	repo, err := openRepository(ctx, path, spec.Repository, spec.Branch, authMethod)
	if err != nil {
		return nil, err
	}

	return &GoGitRepository{
		branch:     spec.Branch,
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
