package gitops

import (
	"context"
	"fmt"
	"path/filepath"

	gitx "github.com/artarts36/swarm-deploy/internal/git"
)

type SyncResult struct {
	Updated     bool
	OldRevision string
	NewRevision string
}

type Syncer struct {
	repositoryDir string
	repository    gitx.Repository
}

func NewSyncer(repository gitx.Repository, dataDir string) *Syncer {
	return &Syncer{
		repositoryDir: filepath.Join(dataDir, "repo"),
		repository:    repository,
	}
}

func (s *Syncer) RepositoryDir() string {
	return s.repositoryDir
}

func (s *Syncer) WorkingDir() string {
	return s.repositoryDir
}

func (s *Syncer) Sync(ctx context.Context) (SyncResult, error) {
	oldHead, err := s.repository.Head(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve old head: %w", err)
	}

	err = s.repository.Pull(ctx)
	if err != nil {
		return SyncResult{}, err
	}

	newHead, err := s.repository.Head(ctx)
	if err != nil {
		return SyncResult{}, fmt.Errorf("resolve new head: %w", err)
	}

	return SyncResult{
		Updated:     oldHead != newHead,
		OldRevision: oldHead,
		NewRevision: newHead,
	}, nil
}
