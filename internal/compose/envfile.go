package compose

import (
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"
)

// EnvFile is an environment file loaded from a compose service definition.
type EnvFile struct {
	// Path is the environment file path from the compose definition.
	Path string `json:"path"`
	// Variables contains variables loaded from Path.
	Variables map[string]string `json:"-" yaml:"-"`
}

// EnvFiles is a list of environment files, accepting a single path in Compose YAML.
type EnvFiles []EnvFile

// UnmarshalYAML accepts an env_file path or a list of paths.
func (f *EnvFiles) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			*f = nil
			return nil
		}

		var path string
		if err := node.Decode(&path); err != nil {
			return err
		}
		*f = EnvFiles{{Path: path}}
		return nil
	case yaml.SequenceNode:
		var files []EnvFile
		if err := node.Decode(&files); err != nil {
			return err
		}
		*f = files
		return nil
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
		return fmt.Errorf("env_file must be a path or a list of paths")
	default:
		return fmt.Errorf("env_file has invalid YAML node kind %d", node.Kind)
	}
}

// UnmarshalYAML decodes an environment file path.
func (f *EnvFile) UnmarshalYAML(node *yaml.Node) error {
	return node.Decode(&f.Path)
}

// MarshalYAML encodes an environment file as its compose path.
func (f EnvFile) MarshalYAML() (interface{}, error) {
	return f.Path, nil
}

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
func (p *EnvFilePopulator) Populate(file *File) bool {
	changed := false

	for index := range file.Compose.Services {
		populated := p.populateService(&file.Compose.Services[index])
		changed = changed || populated
	}

	return changed
}

func (p *EnvFilePopulator) populateService(service *Service) bool {
	if len(service.EnvFiles) == 0 {
		return false
	}

	effective := make(map[string]string)

	for _, envFile := range service.EnvFiles {
		for key, value := range envFile.Variables {
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

	return true
}
