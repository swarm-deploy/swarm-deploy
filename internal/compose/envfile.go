package compose

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// EnvFilePopulator resolves service env_file entries into the effective environment.
//
// Files are applied in declaration order, so values from later env files override
// values from earlier files. Explicit service environment values are applied last.
// After population, env_file is cleared so downstream reconciliation works with a
// canonical, self-contained desired state.
type EnvFilePopulator struct {
	fileReader func(ctx context.Context, path string) ([]byte, error)
	lookupEnv  func(key string) (string, bool)
}

// NewEnvFilePopulator builds an env_file populator backed by the provided file reader.
func NewEnvFilePopulator(reader func(ctx context.Context, path string) ([]byte, error)) *EnvFilePopulator {
	return newEnvFilePopulator(reader, os.LookupEnv)
}

func newEnvFilePopulator(
	reader func(ctx context.Context, path string) ([]byte, error),
	lookupEnv func(key string) (string, bool),
) *EnvFilePopulator {
	return &EnvFilePopulator{
		fileReader: reader,
		lookupEnv:  lookupEnv,
	}
}

// Populate resolves env_file values for all services in file.
//
// It returns true when at least one service contained env_file entries.
func (p *EnvFilePopulator) Populate(ctx context.Context, file *File) (bool, error) {
	baseDir := filepath.Dir(file.Path)
	changed := false

	for index, service := range file.Compose.Services {
		if len(service.EnvFiles) == 0 {
			continue
		}

		effective := make(map[string]string)

		for _, envFile := range service.EnvFiles {
			path := envFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, envFile)
			}

			content, err := p.fileReader(ctx, path)
			if err != nil {
				return false, fmt.Errorf(
					"read env_file %s for service %q: %w",
					path,
					service.Name,
					err,
				)
			}

			values, err := parseEnvFile(content, p.lookupEnv)
			if err != nil {
				return false, fmt.Errorf(
					"parse env_file %s for service %q: %w",
					path,
					service.Name,
					err,
				)
			}

			for key, value := range values {
				effective[key] = p.resolveValue(key, value)
			}
		}

		for key, value := range service.Environment.Map {
			effective[key] = p.resolveValue(key, value)
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

		file.Compose.Services[index] = service
		changed = true
	}

	return changed, nil
}

func (p *EnvFilePopulator) resolveValue(key, value string) string {
	if value != "" || p.lookupEnv == nil {
		return value
	}

	if resolved, found := p.lookupEnv(key); found {
		return resolved
	}

	return value
}

// parseEnvFile follows the env-file syntax used by Docker stack deploy:
// comments and empty lines are ignored, leading whitespace is stripped,
// values are otherwise kept as-is, and a key without "=" is resolved from
// the process environment when present.
func parseEnvFile(
	content []byte,
	lookupEnv func(key string) (string, bool),
) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	utf8BOM := []byte{0xEF, 0xBB, 0xBF}

	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		lineBytes := scanner.Bytes()
		if !utf8.Valid(lineBytes) {
			return nil, fmt.Errorf("invalid utf8 bytes at line %d", lineNumber)
		}

		if lineNumber == 1 {
			lineBytes = bytes.TrimPrefix(lineBytes, utf8BOM)
		}

		line := strings.TrimLeftFunc(string(lineBytes), unicode.IsSpace)
		if line == "" || line[0] == '#' {
			continue
		}

		key, value, hasValue := strings.Cut(line, "=")
		if key == "" {
			return nil, fmt.Errorf("no variable name at line %d", lineNumber)
		}
		if strings.ContainsAny(key, " \t") {
			return nil, fmt.Errorf("variable %q contains whitespace at line %d", key, lineNumber)
		}

		if hasValue {
			values[key] = value
			continue
		}

		if lookupEnv != nil {
			if value, found := lookupEnv(key); found {
				values[key] = value
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}
