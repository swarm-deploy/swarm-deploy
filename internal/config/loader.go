package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/artarts36/specw"
	"gopkg.in/yaml.v3"
)

func Unmarshal(path string) (*Config, error) {
	specw.SetFileReader(func(string) ([]byte, error) {
		return []byte(path), nil
	})

	return Load(path)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &Config{}
	err = yaml.Unmarshal(data, cfg)
	if err != nil {
		return nil, fmt.Errorf("decode config yaml: %w", err)
	}

	configDir := filepath.Dir(path)

	err = cfg.applyDefaults(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.loadStacks(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.loadNetworks(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
