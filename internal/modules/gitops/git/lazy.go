package git

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

type LazyProxy struct {
	spec config.GitSpec
	path string

	initializing chan struct{}
	repository   *GoGitRepository
}

func NewLazyProxy(spec config.GitSpec, path string) *LazyProxy {
	return &LazyProxy{
		spec:         spec,
		path:         path,
		initializing: make(chan struct{}, 1),
	}
}

func (p *LazyProxy) WorkingDir() string {
	return p.path
}

func (p *LazyProxy) ReadFile(ctx context.Context, revision, path string) ([]byte, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return []byte{}, err
	}

	return repo.ReadFile(ctx, revision, path)
}

func (p *LazyProxy) Pull(ctx context.Context) (PullResult, error) {
	ctx, cancel := context.WithTimeout(ctx, gitPullTimeout(p.spec))
	defer cancel()

	repo, err := p.init(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return PullResult{}, &PullError{category: FailureOther, diagnostic: repositoryLockTimeoutDiagnostic}
		}
		return PullResult{}, ClassifyPullError(err, p.spec.Repository, p.spec.Auth.Type)
	}

	return repo.Pull(ctx)
}

func (p *LazyProxy) Head(ctx context.Context) (string, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return "", err
	}

	return repo.Head(ctx)
}

func (p *LazyProxy) List(ctx context.Context, limit int) ([]CommitMeta, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return nil, err
	}

	return repo.List(ctx, limit)
}

func (p *LazyProxy) Diff(ctx context.Context, oldRevision string, newRevision string) ([]CommitFileDiff, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return nil, err
	}

	return repo.Diff(ctx, oldRevision, newRevision)
}

func (p *LazyProxy) Show(ctx context.Context, commitHash string) (Commit, error) {
	repo, err := p.init(ctx)
	if err != nil {
		return Commit{}, err
	}

	return repo.Show(ctx, commitHash)
}

func (p *LazyProxy) init(ctx context.Context) (*GoGitRepository, error) {
	ctx, cancel := context.WithTimeout(ctx, gitPullTimeout(p.spec))
	defer cancel()

	select {
	case p.initializing <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-p.initializing
		return nil, err
	}
	if p.repository != nil {
		repo := p.repository
		<-p.initializing
		return repo, nil
	}

	// go-git's SSH setup can block past the context deadline. Let callers
	// return on cancellation, but keep the gate until cloning has stopped.
	result := make(chan error, 1)
	go func() {
		defer func() { <-p.initializing }()
		repo, err := NewGoGitRepository(ctx, p.spec, p.path)
		if err == nil {
			p.repository = repo
		}
		result <- err
	}()

	select {
	case err := <-result:
		if err != nil {
			return nil, err
		}
		return p.repository, nil
	case <-ctx.Done():
		return nil, ClassifyPullError(ctx.Err(), p.spec.Repository, p.spec.Auth.Type)
	}
}

// Branch returns the current branch, or an empty string for detached HEAD.
func (p *LazyProxy) Branch(ctx context.Context) (string, error) {
	return localBranch(ctx, p.path)
}

// Shallow reports whether history has shallow boundaries.
func (p *LazyProxy) Shallow(ctx context.Context) (bool, error) {
	return localShallow(ctx, p.path)
}

// CountCommits counts reachable commits at the given revision, stopping at shallow boundaries.
func (p *LazyProxy) CountCommits(ctx context.Context, revision string) (int, error) {
	return localCountCommits(ctx, p.path, revision)
}

// GitSize sums file sizes in the local Git storage, including packfiles.
func (p *LazyProxy) GitSize(ctx context.Context) (int64, error) {
	return localGitSize(ctx, p.path)
}

// WorkingTreeSize sums working tree file sizes, excluding .git and without following symlinks.
func (p *LazyProxy) WorkingTreeSize(ctx context.Context) (int64, error) {
	return localWorkingTreeSize(ctx, p.path)
}

// TrackedFiles counts distinct paths in the current Git index.
func (p *LazyProxy) TrackedFiles(ctx context.Context) (int, error) {
	return localTrackedFiles(ctx, p.path)
}

// Status reads tracked changes and a bounded list of untracked files, respecting ignore rules.
func (p *LazyProxy) Status(ctx context.Context, limit int) (WorktreeStatus, error) {
	return localStatus(ctx, p.path, limit)
}
