package compose

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/dotenv"
)

// EnvFilePopulator resolves service env_file entries into the effective environment.
//
// Files are applied in declaration order, so values from later env files override
// values from earlier files. Explicit service environment values are applied last.
// After population, env_file is cleared so downstream reconciliation works with a
// canonical, self-contained desired state.
type EnvFilePopulator struct {
}

// NewEnvFilePopulator builds an env_file populator.
func NewEnvFilePopulator() *EnvFilePopulator {
	return &EnvFilePopulator{}
}

// Populate resolves env_file values for all services in file.
//
// It returns true when at least one service contained env_file entries.
func (p *EnvFilePopulator) Populate(
	ctx context.Context,
	file *File,
	reader func(ctx context.Context, path string) ([]byte, error),
) (bool, error) {
	baseDir := filepath.Dir(file.Path)
	changed := false

	for index := range file.Compose.Services {
		populated, err := p.populateService(ctx, baseDir, &file.Compose.Services[index], reader)
		if err != nil {
			return false, err
		}

		changed = changed || populated
	}

	return changed, nil
}

func (p *EnvFilePopulator) populateService(
	ctx context.Context,
	baseDir string,
	service *Service,
	reader func(ctx context.Context, path string) ([]byte, error),
) (bool, error) {
	if len(service.EnvFiles) == 0 {
		return false, nil
	}

	effective := make(map[string]string)

	for _, envFile := range service.EnvFiles {
		path := envFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, envFile)
		}

		content, err := reader(ctx, path)
		if err != nil {
			return false, fmt.Errorf(
				"read env_file %s for service %q: %w",
				path,
				service.Name,
				err,
			)
		}

		values, err := dotenv.Parse(content)
		if err != nil {
			return false, fmt.Errorf(
				"parse env_file %s for service %q: %w",
				path,
				service.Name,
				err,
			)
		}

		for key, value := range values {
			effective[key] = value
		}
	}

	for key, value := range service.Environment.Map {
		effective[key] = value
	}

	keys := make([]string, 0, len(effective))
	for key := range effective {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	service.Environment = Environment{
		Map:   effective,
		Keys:  keys,
		isMap: true,
	}
	service.EnvFiles = nil

	return true, nil
}
