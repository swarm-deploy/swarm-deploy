package git

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/config"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthResolverResolveAnonymous(t *testing.T) {
	resolver := NewAuthResolver()

	testCases := []string{
		"",
		"none",
		" NoNe ",
	}

	for _, authType := range testCases {
		authMethod, err := resolver.Resolve(config.GitAuthSpec{
			Type: authType,
		})
		require.NoError(t, err, "resolve auth for type %q", authType)
		assert.Nil(t, authMethod, "auth method should be nil for anonymous type %q", authType)
	}
}

func TestAuthResolverResolveHTTPWithUsernameAndPassword(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "http",
		HTTP: config.GitHTTPAuth{
			Username: "robot",
			Password: "secret",
		},
	})
	require.NoError(t, err, "resolve http auth")

	basicAuth, ok := authMethod.(*githttp.BasicAuth)
	require.True(t, ok, "auth method should be *http.BasicAuth")
	assert.Equal(t, "robot", basicAuth.Username, "unexpected http username")
	assert.Equal(t, "secret", basicAuth.Password, "unexpected http password")
}

func TestAuthResolverResolveHTTPWithToken(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "http",
		HTTP: config.GitHTTPAuth{
			Token: "token-value",
		},
	})
	require.NoError(t, err, "resolve http auth with token")

	basicAuth, ok := authMethod.(*githttp.BasicAuth)
	require.True(t, ok, "auth method should be *http.BasicAuth")
	assert.Equal(t, "oauth2", basicAuth.Username, "token auth should use oauth2 username")
	assert.Equal(t, "token-value", basicAuth.Password, "token auth should use token as password")
}

func TestAuthResolverResolveHTTPRequiresCredentials(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "http",
		HTTP: config.GitHTTPAuth{},
	})
	require.Error(t, err, "resolve http auth without credentials must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "http auth requires non-empty username and password/token", "unexpected error")
}

func TestAuthResolverResolveUnsupportedType(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "kerberos",
	})
	require.Error(t, err, "unsupported auth type must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "unsupported git auth type", "unexpected error")
}

func TestAuthResolverResolveSSHRequiresPrivateKeyPath(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "ssh",
		SSH:  config.GitSSHAuthSpec{},
	})
	require.Error(t, err, "ssh auth without privateKeyPath must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "ssh auth requires privateKeyPath", "unexpected error")
}

func TestAuthResolverResolveSSHReturnsPrivateKeyReadError(t *testing.T) {
	resolver := NewAuthResolver()

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "ssh",
		SSH: config.GitSSHAuthSpec{
			PrivateKeyPath: filepath.Join(t.TempDir(), "missing-id_rsa"),
		},
	})
	require.Error(t, err, "ssh auth with unreadable private key must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "read ssh private key from file", "unexpected error")
}

func TestAuthResolverResolveSSHWithInsecureIgnoreHostKey(t *testing.T) {
	resolver := NewAuthResolver()
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "ssh",
		SSH: config.GitSSHAuthSpec{
			User:                  "deploy",
			PrivateKeyPath:        keyPath,
			InsecureIgnoreHostKey: true,
		},
	})
	require.NoError(t, err, "resolve ssh auth with insecure host key")

	publicKeys, ok := authMethod.(*gitssh.PublicKeys)
	require.True(t, ok, "auth method should be *ssh.PublicKeys")
	assert.Equal(t, "deploy", publicKeys.User, "unexpected ssh user")
	assert.NotNil(t, publicKeys.HostKeyCallback, "host key callback should be set")
}

func TestAuthResolverResolveSSHWithKnownHostsCallbackError(t *testing.T) {
	resolver := NewAuthResolver()
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "ssh",
		SSH: config.GitSSHAuthSpec{
			PrivateKeyPath: keyPath,
			KnownHostsPath: filepath.Join(t.TempDir(), "missing-known-hosts"),
		},
	})
	require.Error(t, err, "ssh auth with unreadable known_hosts must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "build known_hosts callback", "unexpected error")
}

func TestAuthResolverResolveSSHUsesDefaultUser(t *testing.T) {
	resolver := NewAuthResolver()
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolver.Resolve(config.GitAuthSpec{
		Type: "ssh",
		SSH: config.GitSSHAuthSpec{
			PrivateKeyPath: keyPath,
		},
	})
	require.NoError(t, err, "resolve ssh auth with default user")

	publicKeys, ok := authMethod.(*gitssh.PublicKeys)
	require.True(t, ok, "auth method should be *ssh.PublicKeys")
	assert.Equal(t, "git", publicKeys.User, "default ssh user should be git")
}

func writePrivateKeyFile(t *testing.T, dir string) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err, "generate rsa private key")

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	require.NotEmpty(t, privateKeyPEM, "pem payload must not be empty")

	keyPath := filepath.Join(dir, "id_rsa")
	err = os.WriteFile(keyPath, privateKeyPEM, 0o600)
	require.NoError(t, err, "write private key file")

	return keyPath
}
