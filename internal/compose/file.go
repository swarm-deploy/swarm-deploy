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
	Path     string  `json:"path"`
	RawBytes []byte  `json:"-"`
	Compose  Compose `json:"compose"`
	Digest   string  `json:"digest"`
}

type FileLoader struct {
}

func NewFileLoader() *FileLoader {
	return &FileLoader{}
}

func (f *File) MarshalYAML() ([]byte, error) {
	payload, err := yaml.Marshal(f.Compose)
	if err != nil {
		return nil, fmt.Errorf("marshal compose yaml: %w", err)
	}
	return payload, nil
}

func (l *FileLoader) Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file %s: %w", path, err)
	}

	schema := Compose{}
	err = yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	l.linkServices(&schema)

	file := &File{
		Path:     path,
		RawBytes: raw,
		Compose:  schema,
	}

	digest, err := l.computeDigest(*file)
	if err != nil {
		return nil, fmt.Errorf("compute digest: %w", err)
	}

	file.Digest = digest

	return file, nil
}

func (*FileLoader) linkServices(compose *Compose) {
	for name, service := range compose.Services {
		service.Networks = resolveNetworkAliases(service.Networks, compose.Networks)

		initJobs := normalizeInitJobs(service.InitJobs, compose.Networks)
		service.InitJobs = initJobs

		compose.Services[name] = service
	}
}

func (l *FileLoader) computeDigest(file File) (string, error) {
	baseDir := filepath.Dir(file.Path)
	hasher := sha256.New()
	hasher.Write(file.RawBytes)

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
