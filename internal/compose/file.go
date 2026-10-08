package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/dotenv"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.yaml.in/yaml/v3"
)

// File is a parsed compose file with source metadata.
type File struct {
	// Path is the source compose file path.
	Path string `json:"path"`
	// Compose is the parsed compose specification.
	Compose Compose `json:"compose"`
	// Digest is the content hash including referenced config, secret, and env files.
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
		return nil, &ReadComposeError{FilePath: path, Err: err}
	}

	var syntax yaml.Node
	if err = yaml.Unmarshal(raw, &syntax); err != nil {
		return nil, &ParseComposeError{FilePath: path, Err: err}
	}

	schema := Compose{}
	if err = syntax.Decode(&schema); err != nil {
		return nil, &ValidateComposeError{FilePath: path, Issues: decodeIssues(err), Err: err}
	}

	if issues := l.linkServices(&schema, filepath.Dir(path)); len(issues) > 0 {
		return nil, &ValidateComposeError{FilePath: path, Issues: issues}
	}

	if err = l.loadEnvFiles(ctx, filepath.Dir(path), schema.Services); err != nil {
		return nil, fmt.Errorf("load env files: %w", err)
	}

	configFiles, err := loadObjectFiles(
		ctx, l.fileReader, filepath.Dir(path), "configs", schema.Configs,
		func(config *Config) (string, bool) { return config.File, config.External },
		setConfigData,
	)
	if err != nil {
		return nil, fmt.Errorf("load config files: %w", err)
	}

	secretFiles, err := loadObjectFiles(
		ctx, l.fileReader, filepath.Dir(path), "secrets", schema.Secrets,
		func(secret *Secret) (string, bool) { return secret.File, secret.External },
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("load secret files: %w", err)
	}

	file := &File{
		Path:    path,
		Compose: schema,
	}

	file.Digest = computeDigest(*file, raw, configFiles, secretFiles)

	return file, nil
}

func setConfigData(config *Config, content []byte) {
	config.Data = make([]byte, len(content))
	copy(config.Data, content)
}

func (l *fileLoader) loadEnvFiles(ctx context.Context, baseDir string, services Services) error {
	for serviceIndex := range services {
		service := &services[serviceIndex]
		for envFileIndex := range service.EnvFiles {
			envFile := &service.EnvFiles[envFileIndex]
			path := envFile.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}

			content, err := l.fileReader(ctx, path)
			if err != nil {
				return fmt.Errorf("read env_file %s for service %q: %w", path, service.Name, err)
			}

			envFile.Variables, err = dotenv.Parse(content)
			if err != nil {
				return fmt.Errorf("parse env_file %s for service %q: %w", path, service.Name, err)
			}
		}
	}

	return nil
}

// decodeIssues converts a decode error of syntactically valid YAML into validation issues.
func decodeIssues(err error) []ValidationIssue {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) {
		issues := make([]ValidationIssue, 0, len(typeErr.Errors))
		for _, msg := range typeErr.Errors {
			issues = append(issues, ValidationIssue{
				ResourceType: "compose",
				Code:         IssueCodeInvalidType,
				Message:      msg,
			})
		}
		return issues
	}

	return []ValidationIssue{{
		ResourceType: "compose",
		Code:         IssueCodeInvalidValue,
		Message:      err.Error(),
	}}
}

func (*fileLoader) linkServices(compose *Compose, baseDir string) []ValidationIssue {
	var issues []ValidationIssue

	for ind, service := range compose.Services {
		resolveNetworkAliases(service.Networks, compose.Networks)

		for configIndex := range service.Configs {
			ref := &service.Configs[configIndex]
			shared, ok := compose.Configs[ref.Source]
			if !ok || shared == nil || shared.External || shared.File == "" {
				continue
			}

			ref.File = shared.File
			if !filepath.IsAbs(ref.File) {
				ref.File = filepath.Join(baseDir, ref.File)
			}
		}

		initJobs, jobIssues := normalizeInitJobs(service.InitJobs, compose.Networks)
		for _, issue := range jobIssues {
			issue.ResourceType = "service"
			issue.ResourceName = service.Name
			issues = append(issues, issue)
		}
		service.InitJobs = initJobs

		compose.Services[ind] = service
	}

	return issues
}

type objectFileContents map[string][]byte

func computeDigest(
	file File,
	raw []byte,
	configFiles objectFileContents,
	secretFiles objectFileContents,
) string {
	hasher := sha256.New()
	hasher.Write(raw)

	computeObjectFilesDigest(
		hasher, "configs", file.Compose.Configs, configFiles,
		func(config *Config) (string, string, bool) { return config.Name, config.File, config.External },
	)

	computeObjectFilesDigest(
		hasher, "secrets", file.Compose.Secrets, secretFiles,
		func(secret *Secret) (string, string, bool) { return secret.Name, secret.File, secret.External },
	)

	computeEnvFilesDigest(hasher, file.Compose.Services)

	return hex.EncodeToString(hasher.Sum(nil))
}

func loadObjectFiles[T any, Objects ~map[string]*T](
	ctx context.Context,
	fileReader func(context.Context, string) ([]byte, error),
	baseDir string,
	objectType string,
	objects Objects,
	properties func(*T) (file string, external bool),
	setContent func(*T, []byte),
) (objectFileContents, error) {
	contents := make(objectFileContents, len(objects))
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		object := objects[alias]
		file, external := properties(object)
		if external || file == "" {
			continue
		}

		path := resolveResourceFilePath(baseDir, file)
		content, err := fileReader(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("read %s file %s: %w", objectType, path, err)
		}
		contents[alias] = content
		if setContent != nil {
			setContent(object, content)
		}
	}

	return contents, nil
}

func computeObjectFilesDigest[T any, Objects ~map[string]*T](
	hasher hash.Hash,
	objectType string,
	objects Objects,
	contents objectFileContents,
	properties func(*T) (name string, file string, external bool),
) {
	aliases := make([]string, 0, len(contents))
	for alias := range contents {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		object := objects[alias]
		name, file, _ := properties(object)
		hasher.Write([]byte(objectType))
		hasher.Write([]byte(alias))
		hasher.Write([]byte(name))
		hasher.Write([]byte(file))
		hasher.Write(contents[alias])
	}
}

func resolveResourceFilePath(baseDir string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(baseDir, path)
}

func computeEnvFilesDigest(hasher hash.Hash, services Services) {
	for _, service := range services {
		for i, envFile := range service.EnvFiles {
			hasher.Write([]byte("env_file"))
			hasher.Write([]byte(service.Name))
			fmt.Fprintf(hasher, "%d", i)
			hasher.Write([]byte(envFile.Path))

			keys := make([]string, 0, len(envFile.Variables))
			for key := range envFile.Variables {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				hasher.Write([]byte(key))
				hasher.Write([]byte(envFile.Variables[key]))
			}
		}
	}
}
