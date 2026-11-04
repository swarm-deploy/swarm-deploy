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
	mu       sync.Mutex
	config   dockerAuthConfigFile
	path     string
	envValue string
	fileInfo os.FileInfo
	loaded   bool
}

type NopAuthManager struct{}

func NewAuthManager() AuthManager {
	return &DockerConfigAuthManager{}
}

func (m *DockerConfigAuthManager) ResolveImage(image string) (string, error) {
	dockerConfig, hasAuth, err := m.loadDockerAuthConfigFile()
	if err != nil {
		return "", fmt.Errorf("load docker registry auth: %w", err)
	}
	if !hasAuth {
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

// loadDockerAuthConfigFile caches parsed credentials while the source is unchanged.
// A file's identity, modification time, and size are checked on every lookup.
func (m *DockerConfigAuthManager) loadDockerAuthConfigFile() (dockerAuthConfigFile, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if raw := strings.TrimSpace(os.Getenv("DOCKER_AUTH_CONFIG")); raw != "" {
		if m.loaded && m.envValue == raw {
			return m.config, len(m.config.Auths) > 0, nil
		}
		config, hasAuth, err := decodeDockerAuthConfig([]byte(raw))
		if err != nil {
			m.clearCache()
			return dockerAuthConfigFile{}, false, err
		}
		m.config = config
		m.path = ""
		m.envValue = raw
		m.fileInfo = nil
		m.loaded = true
		return config, hasAuth, nil
	}

	configPath, ok, err := resolveDockerConfigPath()
	if err != nil {
		m.clearCache()
		return dockerAuthConfigFile{}, false, err
	}
	if !ok {
		m.clearCache()
		return dockerAuthConfigFile{}, false, nil
	}

	info, err := os.Stat(configPath)
	if errors.Is(err, os.ErrNotExist) {
		m.clearCache()
		return dockerAuthConfigFile{}, false, nil
	}
	if err != nil {
		m.clearCache()
		return dockerAuthConfigFile{}, false, fmt.Errorf("stat docker auth config %s: %w", configPath, err)
	}
	if !info.Mode().IsRegular() {
		m.clearCache()
		return dockerAuthConfigFile{}, false, fmt.Errorf("read docker auth config %s: not a regular file", configPath)
	}
	if m.loaded && m.path == configPath && m.fileInfo != nil &&
		os.SameFile(info, m.fileInfo) && info.Size() == m.fileInfo.Size() &&
		info.ModTime().Equal(m.fileInfo.ModTime()) {
		return m.config, len(m.config.Auths) > 0, nil
	}

	configBytes, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		m.clearCache()
		return dockerAuthConfigFile{}, false, nil
	}
	if err != nil {
		m.clearCache()
		return dockerAuthConfigFile{}, false, fmt.Errorf("read docker auth config %s: %w", configPath, err)
	}
	config, hasAuth, err := decodeDockerAuthConfig(configBytes)
	if err != nil {
		m.clearCache()
		return dockerAuthConfigFile{}, false, err
	}

	// Do not cache a snapshot if the file changed while it was being read.
	after, err := os.Stat(configPath)
	if err != nil {
		m.clearCache()
		return dockerAuthConfigFile{}, false, fmt.Errorf("stat docker auth config %s after read: %w", configPath, err)
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		m.clearCache()
		return dockerAuthConfigFile{}, false, fmt.Errorf("docker auth config %s changed during read", configPath)
	}

	m.config = config
	m.path = configPath
	m.envValue = ""
	m.fileInfo = after
	m.loaded = true
	return config, hasAuth, nil
}

func (m *DockerConfigAuthManager) clearCache() {
	m.config = dockerAuthConfigFile{}
	m.path = ""
	m.envValue = ""
	m.fileInfo = nil
	m.loaded = false
}

func decodeDockerAuthConfig(configBytes []byte) (dockerAuthConfigFile, bool, error) {
	authConfig := dockerAuthConfigFile{}
	if err := json.Unmarshal(configBytes, &authConfig); err != nil {
		return dockerAuthConfigFile{}, false, fmt.Errorf("decode docker auth config: %w", err)
	}
	return authConfig, len(authConfig.Auths) > 0, nil
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
