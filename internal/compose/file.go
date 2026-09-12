package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"gopkg.in/yaml.v3"
)

// File is a parsed compose file with source metadata.
type File struct {
	// Path is the source compose file path.
	Path string `json:"path"`
	// Compose is the parsed compose specification.
	Compose Compose `json:"compose"`
	// Digest is the content hash including referenced config and secret files.
	Digest string `json:"digest"`
}

// FileLoader loads compose files.
type FileLoader interface {
	// Load reads, decodes, normalizes, and digests a compose file.
	Load(ctx context.Context, path string) (*File, error)
}

type fileLoader struct {
	fileReader func(ctx context.Context, path string) ([]byte, error)
}

// NewFileLoader builds a compose file loader backed by the local filesystem.
func NewFileLoader() FileLoader {
	return NewFileLoaderWithReader(readFile)
}

// NewFileLoaderWithReader builds a compose file loader backed by the provided file reader.
func NewFileLoaderWithReader(reader func(ctx context.Context, path string) ([]byte, error)) FileLoader {
	loader := &fileLoader{
		fileReader: reader,
	}

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return loader
	}

	return NewTraceableFileLoader(tp, loader)
}

func readFile(_ context.Context, path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return content, nil
}

func (f *File) MarshalYAML() ([]byte, error) {
	payload, err := yaml.Marshal(f.Compose)
	if err != nil {
		return nil, fmt.Errorf("marshal compose yaml: %w", err)
	}
	return payload, nil
}

func (l *fileLoader) Load(ctx context.Context, path string) (*File, error) {
	raw, err := l.fileReader(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read compose file %s: %w", path, err)
	}

	schema := Compose{}
	err = yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	if err = l.linkServices(&schema); err != nil {
		return nil, fmt.Errorf("link services: %w", err)
	}

	file := &File{
		Path:    path,
		Compose: schema,
	}

	digest, err := l.computeDigest(ctx, *file, raw)
	if err != nil {
		return nil, fmt.Errorf("compute digest: %w", err)
	}

	file.Digest = digest

	return file, nil
}

func (*fileLoader) linkServices(compose *Compose) error {
	for ind, service := range compose.Services {
		resolveNetworkAliases(service.Networks, compose.Networks)

		initJobs, err := normalizeInitJobs(service.InitJobs, compose.Networks)
		if err != nil {
			return fmt.Errorf("load init jobs for service %q: %w", service.Name, err)
		}
		service.InitJobs = initJobs

		compose.Services[ind] = service
	}

	return nil
}

func (l *fileLoader) computeDigest(ctx context.Context, file File, raw []byte) (string, error) {
	baseDir := filepath.Dir(file.Path)
	hasher := sha256.New()
	hasher.Write(raw)

	compute := func(objects SharedObjects, objectType string) error {
		objectAliases := make([]string, 0, len(objects))
		for alias := range objects {
			objectAliases = append(objectAliases, alias)
		}
		sort.Strings(objectAliases)

		for _, alias := range objectAliases {
			object := objects[alias]
			if object.External {
				continue
			}

			if object.File == "" {
				continue
			}

			absPath := object.File
			if !filepath.IsAbs(absPath) {
				absPath = filepath.Join(baseDir, object.File)
			}

			content, err := l.fileReader(ctx, absPath)
			if err != nil {
				return fmt.Errorf("read %s file %s for digest: %w", objectType, absPath, err)
			}

			hasher.Write([]byte(objectType))
			hasher.Write([]byte(alias))
			hasher.Write([]byte(object.Name))
			hasher.Write([]byte(object.File))
			hasher.Write(content)
		}

		return nil
	}

	if err := compute(file.Compose.Configs, "configs"); err != nil {
		return "", fmt.Errorf("compute for configs: %w", err)
	}

	if err := compute(file.Compose.Secrets, "secrets"); err != nil {
		return "", fmt.Errorf("compute for secrets: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
