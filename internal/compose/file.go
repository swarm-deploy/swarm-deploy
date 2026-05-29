package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type File struct {
	Path    string  `json:"path"`
	Compose Compose `json:"compose"`
	Digest  string  `json:"digest"`
}

type FileLoader struct {
	fileReader func(path string) ([]byte, error)
}

func NewFileLoader() *FileLoader {
	return &FileLoader{
		fileReader: os.ReadFile,
	}
}

func (f *File) MarshalYAML() ([]byte, error) {
	payload, err := yaml.Marshal(f.Compose)
	if err != nil {
		return nil, fmt.Errorf("marshal compose yaml: %w", err)
	}
	return payload, nil
}

func (l *FileLoader) Load(path string) (*File, error) {
	raw, err := l.fileReader(path)
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

	digest, err := l.computeDigest(*file, raw)
	if err != nil {
		return nil, fmt.Errorf("compute digest: %w", err)
	}

	file.Digest = digest

	return file, nil
}

func (*FileLoader) linkServices(compose *Compose) error {
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

func (l *FileLoader) computeDigest(file File, raw []byte) (string, error) {
	baseDir := filepath.Dir(file.Path)
	hasher := sha256.New()
	hasher.Write(raw)

	compute := func(objects SharedObjects, objectType string) error {
		for _, object := range objects {
			if object.External {
				continue
			}

			if object.File != "" {
				continue
			}

			absPath := filepath.Join(baseDir, object.File)
			content, err := os.ReadFile(absPath)
			if err != nil {
				return fmt.Errorf("read %s file %s for digest: %w", objectType, absPath, err)
			}

			hasher.Write([]byte(objectType))
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
