package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/artarts36/specw"
	"go.yaml.in/yaml/v3"
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	// Decode the spec directly so its nested structs inherit KnownFields.
	if decodeErr := decoder.Decode(&cfg.Spec); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return nil, fmt.Errorf("decode config yaml: %w", decodeErr)
	}

	if decodeErr := decoder.Decode(&Spec{}); !errors.Is(decodeErr, io.EOF) {
		if decodeErr != nil {
			return nil, fmt.Errorf("decode config yaml: %w", decodeErr)
		}
		return nil, errors.New("runtime configuration must contain a single YAML document")
	}

	configDir := filepath.Dir(path)

	err = cfg.applyDefaults(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.loadInitialDesired(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
