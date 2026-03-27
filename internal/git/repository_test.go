package git

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artarts36/swarm-deploy/internal/config"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAuthMethodAnonymous(t *testing.T) {
	testCases := []string{
		"",
		"none",
		" NoNe ",
	}

	for _, authType := range testCases {
		authMethod, err := resolveAuthMethod(config.GitAuthSpec{
			Type: authType,
		})
		require.NoError(t, err, "resolve auth for type %q", authType)
		assert.Nil(t, authMethod, "auth method should be nil for anonymous type %q", authType)
	}
}

func TestResolveAuthMethodHTTPWithUsernameAndPassword(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
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

func TestResolveAuthMethodHTTPWithToken(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
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

func TestResolveAuthMethodHTTPRequiresCredentials(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
		Type: "http",
		HTTP: config.GitHTTPAuth{},
	})
	require.Error(t, err, "http auth without credentials must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "http auth requires non-empty username and password/token", "unexpected error")
}

func TestResolveAuthMethodUnsupportedType(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
		Type: "kerberos",
	})
	require.Error(t, err, "unsupported auth type must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "unsupported git auth type", "unexpected error")
}

func TestResolveAuthMethodSSHRequiresPrivateKeyPath(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
		Type: "ssh",
		SSH:  config.GitSSHAuthSpec{},
	})
	require.Error(t, err, "ssh auth without privateKeyPath must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "ssh auth requires privateKeyPath", "unexpected error")
}

func TestResolveAuthMethodSSHReturnsPrivateKeyReadError(t *testing.T) {
	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
		Type: "ssh",
		SSH: config.GitSSHAuthSpec{
			PrivateKeyPath: filepath.Join(t.TempDir(), "missing-id_rsa"),
		},
	})
	require.Error(t, err, "ssh auth with unreadable private key must fail")
	assert.Nil(t, authMethod, "auth method must be nil on error")
	assert.Contains(t, err.Error(), "read ssh private key from file", "unexpected error")
}

func TestResolveAuthMethodSSHWithInsecureIgnoreHostKey(t *testing.T) {
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
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

func TestResolveAuthMethodSSHWithKnownHostsCallbackError(t *testing.T) {
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
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

func TestResolveAuthMethodSSHUsesDefaultUser(t *testing.T) {
	keyPath := writePrivateKeyFile(t, t.TempDir())

	authMethod, err := resolveAuthMethod(config.GitAuthSpec{
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

func TestNewGoGitRepositoryClonesRepositoryAndStoresGoGitRepository(t *testing.T) {
	sourceRepositoryPath, sourceHead := initSourceRepository(t)
	targetRepositoryPath := filepath.Join(t.TempDir(), "target")

	repository, err := NewGoGitRepository(t.Context(), config.GitSpec{
		Repository: sourceRepositoryPath,
		Branch:     sourceHead.Name().Short(),
	}, targetRepositoryPath)
	require.NoError(t, err, "create go-git repository")
	require.NotNil(t, repository.repository, "go-git repository should be initialized by constructor")

	head, err := repository.Head(t.Context())
	require.NoError(t, err, "resolve head")
	assert.Equal(t, sourceHead.Hash().String(), head, "unexpected repository head")
}

func TestNewGoGitRepositoryOpensExistingRepositoryWithoutClone(t *testing.T) {
	sourceRepositoryPath, sourceHead := initSourceRepository(t)

	repository, err := NewGoGitRepository(t.Context(), config.GitSpec{
		Repository: sourceRepositoryPath,
		Branch:     "main",
	}, sourceRepositoryPath)
	require.NoError(t, err, "create repository over existing path")

	head, err := repository.Head(t.Context())
	require.NoError(t, err, "resolve head on existing repository")
	assert.Equal(t, sourceHead.Hash().String(), head, "unexpected head for existing repository")
}

func TestLazyProxyInitializesRepositoryLazily(t *testing.T) {
	sourceRepositoryPath, sourceHead := initSourceRepository(t)
	targetRepositoryPath := filepath.Join(t.TempDir(), "target")

	proxy := NewLazyProxy(config.GitSpec{
		Repository: sourceRepositoryPath,
		Branch:     sourceHead.Name().Short(),
	}, targetRepositoryPath)

	head, err := proxy.Head(t.Context())
	require.NoError(t, err, "resolve proxy head")
	assert.Equal(t, sourceHead.Hash().String(), head, "unexpected proxy head")

	commit, err := proxy.Show(t.Context(), sourceHead.Hash().String())
	require.NoError(t, err, "show commit via lazy proxy")
	assert.Equal(t, "test", commit.Author, "unexpected commit author")

	err = proxy.Pull(t.Context())
	require.NoError(t, err, "pull lazy proxy repository")
}

func TestGoGitRepositoryShowReturnsCommitMetadataAndFileDiff(t *testing.T) {
	sourceRepositoryPath, sourceHead := initSourceRepository(t)
	sourceRepository, err := gogit.PlainOpen(sourceRepositoryPath)
	require.NoError(t, err, "open source repository")

	worktree, err := sourceRepository.Worktree()
	require.NoError(t, err, "open source repository worktree")

	err = os.WriteFile(filepath.Join(sourceRepositoryPath, "README.md"), []byte("hello world"), 0o600)
	require.NoError(t, err, "rewrite readme")
	err = os.WriteFile(filepath.Join(sourceRepositoryPath, "docker-compose.yaml"),
		[]byte("services:\n  api:\n    image: nginx:1.0\n"), 0o600)
	require.NoError(t, err, "write compose file")

	_, err = worktree.Add("README.md")
	require.NoError(t, err, "git add readme")
	_, err = worktree.Add("docker-compose.yaml")
	require.NoError(t, err, "git add compose")

	commitTime := time.Date(2026, time.March, 27, 1, 2, 3, 0, time.UTC)
	commitHash, err := worktree.Commit("second commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "alice",
			Email: "alice@example.com",
			When:  commitTime,
		},
	})
	require.NoError(t, err, "commit changes")

	repository, err := NewGoGitRepository(t.Context(), config.GitSpec{
		Repository: sourceRepositoryPath,
		Branch:     sourceHead.Name().Short(),
	}, sourceRepositoryPath)
	require.NoError(t, err, "create go-git repository")

	commit, err := repository.Show(t.Context(), commitHash.String())
	require.NoError(t, err, "show commit")
	assert.Equal(t, "alice", commit.Author, "unexpected commit author")
	assert.Equal(t, "alice@example.com", commit.AuthorEmail, "unexpected commit author email")
	assert.Equal(t, commitTime.Unix(), commit.Time.Unix(), "unexpected commit author time")
	require.Len(t, commit.Files, 2, "expected diff by two files")

	diffsByPath := map[string]CommitFileDiff{}
	for _, fileDiff := range commit.Files {
		path := fileDiff.NewPath
		if path == "" {
			path = fileDiff.OldPath
		}
		diffsByPath[path] = fileDiff
	}

	require.Contains(t, diffsByPath, "README.md", "readme diff must exist")
	assert.Equal(t, "hello", diffsByPath["README.md"].OldContent, "unexpected old readme content")
	assert.Equal(t, "hello world", diffsByPath["README.md"].NewContent, "unexpected new readme content")
	assert.Contains(t, diffsByPath["README.md"].Patch, "-hello", "unexpected readme patch")
	assert.Contains(t, diffsByPath["README.md"].Patch, "+hello world", "unexpected readme patch")

	require.Contains(t, diffsByPath, "docker-compose.yaml", "compose diff must exist")
	assert.Empty(t, diffsByPath["docker-compose.yaml"].OldContent, "new compose file must not have old content")
	assert.Equal(
		t,
		"services:\n  api:\n    image: nginx:1.0\n",
		diffsByPath["docker-compose.yaml"].NewContent,
		"unexpected new compose content",
	)
}

func TestGoGitRepositoryShowFailsOnUnknownCommit(t *testing.T) {
	sourceRepositoryPath, sourceHead := initSourceRepository(t)

	repository, err := NewGoGitRepository(t.Context(), config.GitSpec{
		Repository: sourceRepositoryPath,
		Branch:     sourceHead.Name().Short(),
	}, sourceRepositoryPath)
	require.NoError(t, err, "create repository")

	_, err = repository.Show(t.Context(), "0000000000000000000000000000000000000000")
	require.Error(t, err, "show unknown commit")
	assert.Contains(t, err.Error(), "find commit", "unexpected error")
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

func initSourceRepository(t *testing.T) (string, *plumbing.Reference) {
	t.Helper()

	sourceRepositoryPath := filepath.Join(t.TempDir(), "source")
	sourceRepository, err := gogit.PlainInit(sourceRepositoryPath, false)
	require.NoError(t, err, "init source repository")

	filePath := filepath.Join(sourceRepositoryPath, "README.md")
	err = os.WriteFile(filePath, []byte("hello"), 0o600)
	require.NoError(t, err, "write source repository file")

	worktree, err := sourceRepository.Worktree()
	require.NoError(t, err, "open source repository worktree")

	_, err = worktree.Add("README.md")
	require.NoError(t, err, "git add file")

	_, err = worktree.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err, "commit file")

	head, err := sourceRepository.Head()
	require.NoError(t, err, "resolve source repository head")

	return sourceRepositoryPath, head
}
