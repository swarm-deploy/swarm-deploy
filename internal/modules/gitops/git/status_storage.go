package git

import (
	"errors"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage"
)

type statusStorage struct {
	storage.Storer
	index *index.Index
}

func (s statusStorage) Index() (*index.Index, error) {
	return s.index, nil
}

func (s statusStorage) Config() (*config.Config, error) {
	cfg, err := s.Storer.Config()
	if err != nil {
		return nil, err
	}
	for name := range cfg.Submodules {
		module, moduleErr := s.Storer.Module(name)
		if moduleErr != nil {
			return nil, moduleErr
		}
		_, refErr := module.Reference(plumbing.HEAD)
		if errors.Is(refErr, plumbing.ErrReferenceNotFound) {
			// With no local HEAD, go-git should use the index's gitlink.
			// Removing this entry from the in-memory config skips its init path.
			delete(cfg.Submodules, name)
		} else if refErr != nil {
			return nil, refErr
		}
	}
	return cfg, nil
}

func (s statusStorage) Module(name string) (storage.Storer, error) {
	module, err := s.Storer.Module(name)
	if err != nil {
		return nil, err
	}
	return statusModuleStorage{Storer: module}, nil
}

type statusModuleStorage struct {
	storage.Storer
}

// If a module's HEAD disappears after Config checked it, stop go-git's
// initialization at SetReference. The wrapper also hides the Init method.
func (statusModuleStorage) SetReference(*plumbing.Reference) error {
	return billy.ErrReadOnly
}
