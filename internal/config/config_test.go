package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	assert.Equal(t, "https://example.com/repo.git", cfg.Spec.Git.Pull.Repository, "unexpected pull repository")
	assert.Equal(t, "https://example.com/repo.git", cfg.Spec.Git.Push.Repository, "unexpected push repository")
	assert.Equal(t, "main", cfg.Spec.Git.Pull.Branch, "unexpected pull branch")
	assert.Equal(t, "main", cfg.Spec.Git.Push.Branch, "unexpected push branch")
	require.Len(t, cfg.Spec.Stacks, 2, "expected 2 stacks")
	assert.Equal(t, "app", cfg.Spec.Stacks[0].Name, "unexpected first stack")
	assert.Equal(t, "worker", cfg.Spec.Stacks[1].Name, "unexpected second stack")
}

func TestLoadWithGitPullAndPush(t *testing.T) {
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
  pull:
    repository: https://example.com/pull.git
    branch: develop
    auth:
      type: none
  push:
    repository: https://example.com/push.git
    branch: release
    auth:
      type: none
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, "https://example.com/pull.git", cfg.Spec.Git.Pull.Repository, "unexpected pull repository")
	assert.Equal(t, "develop", cfg.Spec.Git.Pull.Branch, "unexpected pull branch")
	assert.Equal(t, "https://example.com/push.git", cfg.Spec.Git.Push.Repository, "unexpected push repository")
	assert.Equal(t, "release", cfg.Spec.Git.Push.Branch, "unexpected push branch")
}

func TestLoadFailsWithGitRepositoryPullAndPush(t *testing.T) {
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
  repository:
    pull:
      repository: https://example.com/pull.git
      branch: develop
      auth:
        type: none
    push:
      repository: https://example.com/push.git
      branch: release
      auth:
        type: none
stacks:
  file: ./stacks.yaml
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		"git.repository as object is not supported; use git.pull and git.push",
		"unexpected error",
	)
}

func TestLoadWithGitPushAPIToken(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	apiTokenPath := filepath.Join(dir, "git-push-api-token")
	require.NoError(t, os.WriteFile(apiTokenPath, []byte("token-value"), 0o600), "write push api token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  pull:
    repository: https://example.com/pull.git
    auth:
      type: none
  push:
    repository: https://example.com/push.git
    auth:
      type: none
    apiTokenPath: %s
stacks:
  file: ./stacks.yaml
`, apiTokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, "token-value", string(cfg.Spec.Git.Push.APIToken.Content), "unexpected push api token")
}

func TestLoadFailsWhenGitPullWithoutPush(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  pull:
    repository: https://example.com/pull.git
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "git.push is required when git.pull is set", "unexpected error")
}

func TestLoadFailsWhenGitPushWithoutPull(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  push:
    repository: https://example.com/push.git
stacks:
  file: ./stacks.yaml
`)
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		require.NoError(t, err, "write config file")
	}

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "git.pull is required when git.push is set", "unexpected error")
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

func TestLoadFailsWhenAssistantEnabledWithoutTokenPath(t *testing.T) {
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
assistant:
  enabled: true
  model:
    name: gpt-4o-mini
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		"assistant.model.openai.apiTokenPath is required when assistant.enabled=true",
		"unexpected error",
	)
}

func TestLoadFailsWhenAssistantTemperatureIsInvalid(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: true
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
      temperature: "oops"
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "assistant.model.openai.temperature", "unexpected error")
}

func TestLoadFailsWhenAssistantMaxTokensIsNotPositive(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: true
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
      maxTokens: "0"
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "assistant.model.openai.maxTokens must be > 0", "unexpected error")
}

func TestLoadAllowsInvalidAssistantModelWhenDisabled(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: false
  tools: ["deploy_sync_trigger", " "]
  model:
    name: ""
    openai:
      apiTokenPath: %s
      temperature: "not-a-number"
      maxTokens: "-1"
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.NoError(t, err, "assistant config must be ignored when disabled")
}

func TestLoadAppliesDefaultAssistantConversationInMemoryTTL(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: true
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(
		t,
		defaultAssistantConversationInMemoryTTL,
		cfg.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value,
		"expected default assistant conversation storage ttl",
	)
	assert.Equal(
		t,
		cfg.Spec.Assistant.Model.Name,
		cfg.Spec.Assistant.Model.EmbeddingName,
		"expected assistant embedding model name fallback to assistant.model.name",
	)
}

func TestLoadUsesAssistantConversationInMemoryTTLSpecifiedInConfig(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: true
  conversation:
    storage:
      inMemory:
        ttl: 90m
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(
		t,
		90*time.Minute,
		cfg.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value,
		"expected assistant conversation storage ttl from config",
	)
}

func TestLoadUsesAssistantEmbeddingModelNameFromConfig(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "assistant_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write assistant token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
stacks:
  file: ./stacks.yaml
assistant:
  enabled: true
  model:
    name: gpt-4o-mini
    embeddingName: text-embedding-3-small
    openai:
      apiTokenPath: %s
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(
		t,
		"text-embedding-3-small",
		cfg.Spec.Assistant.Model.EmbeddingName,
		"expected assistant embedding model name from config",
	)
}
