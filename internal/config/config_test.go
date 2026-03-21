package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/event/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadWithStacksFile(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
  - name: worker
    composeFile: worker/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	require.Len(t, cfg.Spec.Stacks, 2, "expected 2 stacks")
	assert.Equal(t, "app", cfg.Spec.Stacks[0].Name, "unexpected first stack")
	assert.Equal(t, "worker", cfg.Spec.Stacks[1].Name, "unexpected second stack")
}

func TestLoadFailsWithoutStacksFile(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "stacks.file is required", "unexpected error")
}

func TestLoadAllowsMissingStacksFileBeforeFirstSync(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Empty(t, cfg.Spec.Stacks, "stacks must be loaded later from git repository during sync")
}

func TestWebhookSecretResolveFromFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretPath, []byte(" from-file \n"), 0o600); err != nil {
		require.NoError(t, err, "write secret file")
	}

	spec := WebhookSpec{
		SecretPath: secretPath,
	}

	assert.Equal(t, "from-file", spec.ResolveSecret(), "expected secret from file")
}

func TestLoadResolvesRelativeWebhookSecretPath(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	secretPath := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretPath, []byte("from-file"), 0o600); err != nil {
		require.NoError(t, err, "write secret file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: webhook
  webhook:
    enabled: true
    secretPath: ./webhook_secret
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, secretPath, cfg.Spec.Sync.Webhook.SecretPath, "expected resolved secretPath")
	assert.Equal(t, "from-file", cfg.WebhookSecret(), "expected secret from file")
}

func TestLoadResolvesRelativeBasicHTPasswdPath(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	htpasswdPath := filepath.Join(dir, "basic.htpasswd")
	htpasswdContent := []byte(
		"admin:$2a$10$abcdefghijklmnopqrstuu5Lo0M/JD0P7P4nM8JrIoYNewc0hXtNq\n",
	)
	if err := os.WriteFile(htpasswdPath, htpasswdContent, 0o600); err != nil {
		require.NoError(t, err, "write htpasswd file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
web:
  security:
    authentication:
      basic:
        htpasswdFile: ./basic.htpasswd
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(
		t,
		htpasswdPath,
		cfg.Spec.Web.Security.Authentication.Basic.HTPasswdFile,
		"expected resolved htpasswd path",
	)
	assert.Equal(
		t,
		AuthenticationStrategyBasic,
		cfg.Spec.Web.Security.Authentication.Strategy(),
		"expected basic auth strategy",
	)
}

func TestLoadFailsOnMissingBasicHTPasswdFile(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
web:
  security:
    authentication:
      basic:
        htpasswdFile: ./missing.htpasswd
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "web.security.authentication.basic.htpasswdFile", "unexpected error")
}

func TestLoadIgnoresDataDirFromConfig(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	secretPath := filepath.Join(dir, "webhook_secret")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o600); err != nil {
		require.NoError(t, err, "write secret file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
dataDir: /tmp/custom-path-should-be-ignored
git:
  repository: https://example.com/repo.git
sync:
  mode: webhook
  webhook:
    enabled: true
    secretPath: ./webhook_secret
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")

	expectedDataDir := filepath.Join(dir, ".swarm-deploy")
	assert.Equal(t, expectedDataDir, cfg.Spec.DataDir, "expected dataDir")
}

func TestLoadWebAddressUsedForSingleServer(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
web:
  address: ":18080"
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, ":18080", cfg.Spec.Web.Address, "expected web.address")
	assert.Equal(t, defaultWebhookAddress, cfg.Spec.Sync.Webhook.Address, "expected sync.webhook.address")
}

func TestLoadWebAddressDefaults(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, defaultWebAddress, cfg.Spec.Web.Address, "expected web.address")
	assert.Equal(t, defaultWebhookAddress, cfg.Spec.Sync.Webhook.Address, "expected sync.webhook.address")
}

func TestLoadResolvesRelativeGitSSHPassphrasePath(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	if err := os.WriteFile(stacksPath, stacksPayload, 0o600); err != nil {
		require.NoError(t, err, "write stacks file")
	}

	passphrasePath := filepath.Join(dir, "git_passphrase")
	if err := os.WriteFile(passphrasePath, []byte(" super-secret \n"), 0o600); err != nil {
		require.NoError(t, err, "write passphrase file")
	}

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: git@github.com:your-org/your-stacks-repo.git
  auth:
    type: ssh
    ssh:
      privateKeyPath: /run/secrets/deploy_key
      passphrasePath: ./git_passphrase
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, passphrasePath, cfg.Spec.Git.Auth.SSH.PassphrasePath, "expected passphrasePath")

	passphrase, err := cfg.Spec.Git.Auth.SSH.ResolvePassphrase()
	require.NoError(t, err, "resolve passphrase")
	assert.Equal(t, "super-secret", passphrase, "expected passphrase")
}

func TestReloadStacksPrefersFirstAvailableBaseDir(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	repoDir := filepath.Join(dir, "repo")

	require.NoError(t, os.MkdirAll(configDir, 0o755), "create config dir")
	require.NoError(t, os.MkdirAll(repoDir, 0o755), "create repo dir")

	configStacksPath := filepath.Join(configDir, "stacks.yaml")
	repoStacksPath := filepath.Join(repoDir, "stacks.yaml")

	configStacks := []byte(`
stacks:
  - name: from-config
    composeFile: app.yaml
`)
	repoStacks := []byte(`
stacks:
  - name: from-repo
    composeFile: app.yaml
`)

	require.NoError(t, os.WriteFile(configStacksPath, configStacks, 0o600), "write config stacks")
	require.NoError(t, os.WriteFile(repoStacksPath, repoStacks, 0o600), "write repo stacks")

	cfg := &Config{
		Spec: Spec{
			StacksSource: StacksSourceSpec{
				File: "./stacks.yaml",
			},
		},
	}

	loadedFrom, err := cfg.ReloadStacks(repoDir, configDir)
	require.NoError(t, err, "reload stacks")
	assert.Equal(t, repoStacksPath, loadedFrom, "expected repo stacks path")
	require.Len(t, cfg.Spec.Stacks, 1, "expected one stack")
	assert.Equal(t, "from-repo", cfg.Spec.Stacks[0].Name, "expected stack from repo")
}

func TestReloadStacksFallsBackToNextBaseDir(t *testing.T) {
	dir := t.TempDir()
	missingRepoDir := filepath.Join(dir, "repo")
	configDir := filepath.Join(dir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755), "create config dir")

	configStacksPath := filepath.Join(configDir, "stacks.yaml")
	configStacks := []byte(`
stacks:
  - name: from-config
    composeFile: app.yaml
`)
	require.NoError(t, os.WriteFile(configStacksPath, configStacks, 0o600), "write config stacks")

	cfg := &Config{
		Spec: Spec{
			StacksSource: StacksSourceSpec{
				File: "./stacks.yaml",
			},
		},
	}

	loadedFrom, err := cfg.ReloadStacks(missingRepoDir, configDir)
	require.NoError(t, err, "reload stacks")
	assert.Equal(t, configStacksPath, loadedFrom, "expected fallback config stacks path")
	require.Len(t, cfg.Spec.Stacks, 1, "expected one stack")
	assert.Equal(t, "from-config", cfg.Spec.Stacks[0].Name, "expected stack from config")
}

func TestLoadResolvesRelativeTelegramBotTokenPathInNotificationsOn(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "telegram_bot_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write bot token file")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
notifications:
  on:
    deploySuccess:
      telegram:
        - name: ops
          botTokenPath: ./telegram_bot_token
          chatId: "-1001234567890"
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	channels, ok := cfg.Spec.Notifications.On[events.TypeDeploySuccess]
	require.True(t, ok, "expected deploySuccess notifications")
	require.Len(t, channels.Telegram, 1, "expected one telegram channel")
	assert.Equal(
		t,
		tokenPath,
		channels.Telegram[0].BotTokenPath,
		"expected resolved telegram token path",
	)
}

func TestLoadFailsOnCustomNotificationWithoutURLInNotificationsOn(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
notifications:
  on:
    deploySuccess:
      custom:
        - name: audit
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		`notifications.on["deploySuccess"].custom[0].url or urlEnv is required`,
		"unexpected error",
	)
}
