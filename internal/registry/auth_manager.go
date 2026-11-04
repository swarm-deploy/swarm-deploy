package registry

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/distribution/reference"
	dockerregistry "github.com/docker/docker/api/types/registry"
)

const (
	dockerAuthConfigFileName = "config.json"
	usernamePasswordParts    = 2
)

type dockerAuthConfigFile struct {
	Auths map[string]dockerregistry.AuthConfig `json:"auths"`
}

// AuthManager resolves image pull credentials for a container image reference.
type AuthManager interface {
	// ResolveImage returns an encoded Docker registry auth payload for image.
	// Empty string means no auth found or auth is not required.
	ResolveImage(image string) (string, error)
}

type DockerConfigAuthManager struct {
	mu    sync.Mutex
	cache authCache
}

type authCache struct {
	config dockerAuthConfigFile
	env    string
	path   string
	info   os.FileInfo
}

type NopAuthManager struct{}

func NewAuthManager() AuthManager {
	return &DockerConfigAuthManager{}
}

func (m *DockerConfigAuthManager) ResolveImage(image string) (string, error) {
	dockerConfig, err := m.loadConfig()
	if err != nil {
		return "", fmt.Errorf("load docker registry auth: %w", err)
	}
	if len(dockerConfig.Auths) == 0 {
		return "", nil
	}

	registryHost, err := parseRegistryHost(image)
	if err != nil {
		return "", err
	}
	if registryHost == "" {
		return "", nil
	}

	authConfig, found := findRegistryAuthConfig(dockerConfig.Auths, registryHost)
	if !found {
		return "", nil
	}

	authConfig = decodeAuthField(authConfig)
	if authConfig.ServerAddress == "" {
		authConfig.ServerAddress = registryHost
	}

	encodedRegistryAuth, err := dockerregistry.EncodeAuthConfig(authConfig)
	if err != nil {
		return "", fmt.Errorf("encode registry auth: %w", err)
	}

	return encodedRegistryAuth, nil
}

func (m *NopAuthManager) ResolveImage(_ string) (string, error) {
	return "", nil
}

func parseRegistryHost(image string) (string, error) {
	if strings.TrimSpace(image) == "" {
		return "", nil
	}

	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("parse image reference %q: %w", image, err)
	}

	return reference.Domain(named), nil
}

// loadConfig returns the cached config while its source is unchanged.
func (m *DockerConfigAuthManager) loadConfig() (dockerAuthConfigFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		config dockerAuthConfigFile
		err    error
	)
	if raw := strings.TrimSpace(os.Getenv("DOCKER_AUTH_CONFIG")); raw != "" {
		config, err = m.loadEnv(raw)
	} else {
		config, err = m.loadFile()
	}
	if err != nil {
		m.cache = authCache{}
	}
	return config, err
}

func (m *DockerConfigAuthManager) loadEnv(raw string) (dockerAuthConfigFile, error) {
	if m.cache.env == raw {
		return m.cache.config, nil
	}
	config, err := decodeDockerAuthConfig([]byte(raw))
	if err != nil {
		return dockerAuthConfigFile{}, err
	}
	m.cache = authCache{env: raw, config: config}
	return config, nil
}

func (m *DockerConfigAuthManager) loadFile() (dockerAuthConfigFile, error) {
	path, ok, err := resolveDockerConfigPath()
	if err != nil {
		return dockerAuthConfigFile{}, err
	}
	if !ok {
		m.cache = authCache{}
		return dockerAuthConfigFile{}, nil
	}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		m.cache = authCache{}
		return dockerAuthConfigFile{}, nil
	}
	if err != nil {
		return dockerAuthConfigFile{}, fmt.Errorf("stat docker auth config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return dockerAuthConfigFile{}, fmt.Errorf("read docker auth config %s: not a regular file", path)
	}
	if m.cache.matchesFile(path, info) {
		return m.cache.config, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		m.cache = authCache{}
		return dockerAuthConfigFile{}, nil
	}
	if err != nil {
		return dockerAuthConfigFile{}, fmt.Errorf("read docker auth config %s: %w", path, err)
	}
	config, err := decodeDockerAuthConfig(data)
	if err != nil {
		return dockerAuthConfigFile{}, err
	}
	m.cache = authCache{path: path, info: info, config: config}
	return config, nil
}

func (c authCache) matchesFile(path string, info os.FileInfo) bool {
	return c.path == path && c.info != nil &&
		os.SameFile(info, c.info) &&
		info.Size() == c.info.Size() &&
		info.ModTime().Equal(c.info.ModTime())
}

func decodeDockerAuthConfig(data []byte) (dockerAuthConfigFile, error) {
	var config dockerAuthConfigFile
	if err := json.Unmarshal(data, &config); err != nil {
		return dockerAuthConfigFile{}, fmt.Errorf("decode docker auth config: %w", err)
	}
	return config, nil
}

func resolveDockerConfigPath() (string, bool, error) {
	if dockerConfigDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG")); dockerConfigDir != "" {
		return filepath.Join(dockerConfigDir, dockerAuthConfigFileName), true, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve home directory for docker config: %w", err)
	}
	if homeDir == "" {
		return "", false, nil
	}

	return filepath.Join(homeDir, ".docker", dockerAuthConfigFileName), true, nil
}

func findRegistryAuthConfig(
	authConfigs map[string]dockerregistry.AuthConfig,
	registryHost string,
) (dockerregistry.AuthConfig, bool) {
	normalizedRegistryHost := normalizeRegistryHost(registryHost)
	for serverAddress, authConfig := range authConfigs {
		if normalizeRegistryHost(serverAddress) != normalizedRegistryHost {
			continue
		}
		if authConfig.ServerAddress == "" {
			authConfig.ServerAddress = registryHost
		}
		return authConfig, true
	}

	return dockerregistry.AuthConfig{}, false
}

func normalizeRegistryHost(v string) string {
	normalized := strings.TrimSpace(strings.ToLower(v))
	normalized = strings.TrimPrefix(normalized, "https://")
	normalized = strings.TrimPrefix(normalized, "http://")
	normalized = strings.TrimSuffix(normalized, "/")
	if slashIdx := strings.Index(normalized, "/"); slashIdx >= 0 {
		normalized = normalized[:slashIdx]
	}

	switch normalized {
	case "index.docker.io", "registry-1.docker.io":
		return "docker.io"
	default:
		return normalized
	}
}

func decodeAuthField(authConfig dockerregistry.AuthConfig) dockerregistry.AuthConfig {
	if authConfig.Auth == "" || (authConfig.Username != "" && authConfig.Password != "") {
		return authConfig
	}

	decodedRaw, err := base64.StdEncoding.DecodeString(authConfig.Auth)
	if err != nil {
		decodedRaw, err = base64.RawStdEncoding.DecodeString(authConfig.Auth)
		if err != nil {
			return authConfig
		}
	}

	usernameAndPassword := strings.SplitN(string(decodedRaw), ":", usernamePasswordParts)
	if len(usernameAndPassword) != usernamePasswordParts {
		return authConfig
	}

	authConfig.Username = usernameAndPassword[0]
	authConfig.Password = usernameAndPassword[1]
	return authConfig
}
