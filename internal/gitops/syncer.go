package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/config"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SyncResult struct {
	Updated     bool
	OldRevision string
	NewRevision string
}

type Syncer struct {
	repositoryURL string
	branch        string
	repositoryDir string
	repositorySub string
	auth          transport.AuthMethod
}

func NewSyncer(gitSpec config.GitSpec, dataDir string) (*Syncer, error) {
	authMethod, err := buildAuthMethod(gitSpec.Auth)
	if err != nil {
		return nil, err
	}

	return &Syncer{
		repositoryURL: gitSpec.Repository,
		branch:        gitSpec.Branch,
		repositoryDir: filepath.Join(dataDir, "repo"),
		repositorySub: gitSpec.Path,
		auth:          authMethod,
	}, nil
}

func (s *Syncer) RepositoryDir() string {
	return s.repositoryDir
}

func (s *Syncer) WorkingDir() string {
	if s.repositorySub == "" {
		return s.repositoryDir
	}
	return filepath.Join(s.repositoryDir, s.repositorySub)
}

func (s *Syncer) Sync(ctx context.Context) (SyncResult, error) {
	if _, err := os.Stat(filepath.Join(s.repositoryDir, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.clone(ctx)
		}
		return SyncResult{}, fmt.Errorf("stat repository dir: %w", err)
	}

	repo, err := git.PlainOpen(s.repositoryDir)
	if err != nil {
		return SyncResult{}, fmt.Errorf("open repository: %w", err)
	}

	oldHead, err := resolveHead(repo)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve old head: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return SyncResult{}, fmt.Errorf("open worktree: %w", err)
	}

	err = worktree.PullContext(ctx, &git.PullOptions{
		RemoteName:    "origin",
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(s.branch),
		Auth:          s.auth,
		Force:         true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return SyncResult{}, fmt.Errorf("git pull: %w", err)
	}

	newHead, err := resolveHead(repo)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve new head: %w", err)
	}

	return SyncResult{
		Updated:     oldHead != newHead,
		OldRevision: oldHead,
		NewRevision: newHead,
	}, nil
}

func (s *Syncer) clone(ctx context.Context) (SyncResult, error) {
	if err := os.MkdirAll(s.repositoryDir, 0o755); err != nil {
		return SyncResult{}, fmt.Errorf("create repository dir: %w", err)
	}

	repo, err := git.PlainCloneContext(ctx, s.repositoryDir, false, &git.CloneOptions{
		URL:           s.repositoryURL,
		Auth:          s.auth,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(s.branch),
	})
	if err != nil {
		return SyncResult{}, fmt.Errorf("git clone: %w", err)
	}

	head, err := resolveHead(repo)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve clone head: %w", err)
	}

	return SyncResult{
		Updated:     true,
		OldRevision: "",
		NewRevision: head,
	}, nil
}

func resolveHead(repo *git.Repository) (string, error) {
	headRef, err := repo.Head()
	if err != nil {
		return "", err
	}
	return headRef.Hash().String(), nil
}

func buildAuthMethod(auth config.GitAuthSpec) (transport.AuthMethod, error) {
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "none":
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
	passphrase := auth.ResolvePassphrase()

	var (
		pk  *gitssh.PublicKeys
		err error
	)

	if auth.PrivateKeyPath != "" {
		pk, err = gitssh.NewPublicKeysFromFile(user, auth.PrivateKeyPath, passphrase)
		if err != nil {
			return nil, fmt.Errorf("read ssh private key from file: %w", err)
		}
	} else if key := auth.ResolvePrivateKey(); key != "" {
		pk, err = gitssh.NewPublicKeys(user, []byte(key), passphrase)
		if err != nil {
			return nil, fmt.Errorf("read ssh private key from value: %w", err)
		}
	} else {
		return nil, errors.New("ssh auth requires privateKeyPath or privateKey/privateKeyEnv")
	}

	if auth.InsecureIgnoreHostKey {
		pk.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		}
		return pk, nil
	}

	if auth.KnownHostsPath != "" {
		callback, err := knownhosts.New(auth.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("build known_hosts callback: %w", err)
		}
		pk.HostKeyCallbackHelper = gitssh.HostKeyCallbackHelper{
			HostKeyCallback: callback,
		}
	}

	return pk, nil
}
