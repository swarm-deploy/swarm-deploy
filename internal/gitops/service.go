package gitops

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

type Service struct {
	Controller *controller.Controller
	Store      modelstore.Store

	cfg        *config.Config
	filesystem fs.FileSystem
}

func InitService(
	ctx context.Context,
	cfg *config.Config,
	filesystem fs.FileSystem,
) (*Service, error) {
	srv := &Service{
		cfg:        cfg,
		filesystem: filesystem,
	}

	if err := srv.initStore(ctx); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}

	return srv, nil
}

func (s *Service) initStore(ctx context.Context) error {
	fileStore, err := modelstore.NewFileStore(ctx,
		filepath.Join(s.cfg.Spec.DataDir, "controller.state.json"),
		s.filesystem,
	)
	if err != nil {
		return fmt.Errorf("init file store: %w", err)
	}

	warmupStore := modelstore.NewWarmupStore(modelstore.NewMemoryStore(), fileStore)
	warmupStore.Warmup(ctx)

	s.Store = warmupStore

	return nil
}
