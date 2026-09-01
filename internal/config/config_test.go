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

func TestLoadWithNetworksFile(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	networksPath := filepath.Join(dir, "networks.yaml")
	networksPayload := []byte(`
networks:
  - name: app_backend
    labels:
      team: platform
`)
	require.NoError(t, os.WriteFile(networksPath, networksPayload, 0o600), "write networks file")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
networks:
  file: ./networks.yaml
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	require.Len(t, cfg.Spec.Networks, 1, "expected one network")
	assert.Equal(t, "app_backend", cfg.Spec.Networks[0].Name, "unexpected network name")
	assert.Equal(t, "overlay", cfg.Spec.Networks[0].Driver, "unexpected default network driver")
}

func TestLoadAllowsMissingNetworksFileBeforeFirstSync(t *testing.T) {
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
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
networks:
  file: ./networks.yaml
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Empty(t, cfg.Spec.Networks, "networks must be loaded later from git repository during sync")
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

func TestReloadNetworksPrefersFirstAvailableBaseDir(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	repoDir := filepath.Join(dir, "repo")

	require.NoError(t, os.MkdirAll(configDir, 0o755), "create config dir")
	require.NoError(t, os.MkdirAll(repoDir, 0o755), "create repo dir")

	configNetworksPath := filepath.Join(configDir, "networks.yaml")
	repoNetworksPath := filepath.Join(repoDir, "networks.yaml")

	configNetworks := []byte(`
networks:
  - name: from-config
`)
	repoNetworks := []byte(`
networks:
  - name: from-repo
`)

	require.NoError(t, os.WriteFile(configNetworksPath, configNetworks, 0o600), "write config networks")
	require.NoError(t, os.WriteFile(repoNetworksPath, repoNetworks, 0o600), "write repo networks")

	cfg := &Config{
		Spec: Spec{
			NetworksSource: NetworksSourceSpec{
				File: "./networks.yaml",
			},
		},
	}

	loadedFrom, err := cfg.ReloadNetworks(repoDir, configDir)
	require.NoError(t, err, "reload networks")
	assert.Equal(t, repoNetworksPath, loadedFrom, "expected repo networks path")
	require.Len(t, cfg.Spec.Networks, 1, "expected one network")
	assert.Equal(t, "from-repo", cfg.Spec.Networks[0].Name, "expected network from repo")
}

func TestLoadFailsOnManagedNetworkLabelNotTrue(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	networksPath := filepath.Join(dir, "networks.yaml")
	networksPayload := []byte(`
networks:
  - name: app_backend
    labels:
      org.swarm-deploy.network.managed: "false"
`)
	require.NoError(t, os.WriteFile(networksPath, networksPayload, 0o600), "write networks file")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(`
git:
  repository: https://example.com/repo.git
sync:
  mode: pull
stacks:
  file: ./stacks.yaml
networks:
  file: ./networks.yaml
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		`labels["org.swarm-deploy.network.managed"] must be "true"`,
		"unexpected error",
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

func TestLoadFailsOnUnknownNotificationEventType(t *testing.T) {
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
    unknownEvent:
      custom:
        - name: audit
          url: https://example.com/hook
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), `notifications.on["unknownEvent"] has unknown event type`, "unexpected error")
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

func TestLoadFailsOnUnsupportedAssistantTool(t *testing.T) {
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
  tools: ["custom_tool"]
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), `assistant.tools[0] has unsupported tool "custom_tool"`, "unexpected error")
}

func TestLoadFailsOnAssistantToolWithWhitespaces(t *testing.T) {
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
  tools: [" deploy_sync_trigger "]
  model:
    name: gpt-4o-mini
    openai:
      apiTokenPath: %s
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		`assistant.tools[0] has unsupported tool " deploy_sync_trigger "`,
		"unexpected error",
	)
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

func TestLoadFailsWhenGitHTTPAuthCredentialsAreMissing(t *testing.T) {
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
  auth:
    type: http
stacks:
  file: ./stacks.yaml
`)
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(t, err.Error(), "git.auth.http requires username+passwordPath or tokenPath", "unexpected error")
}

func TestLoadFailsWhenGitHTTPAuthHasBothPasswordAndToken(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	passwordPath := filepath.Join(dir, "git_password")
	require.NoError(t, os.WriteFile(passwordPath, []byte("secret"), 0o600), "write git password")
	tokenPath := filepath.Join(dir, "git_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write git token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
  auth:
    type: http
    http:
      username: robot
      passwordPath: %s
      tokenPath: %s
stacks:
  file: ./stacks.yaml
`, passwordPath, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	_, err := Load(configPath)
	require.Error(t, err, "expected error")
	assert.Contains(
		t,
		err.Error(),
		"git.auth.http.tokenPath and git.auth.http.passwordPath are mutually exclusive",
		"unexpected error",
	)
}

func TestLoadSupportsGitHTTPAuthWithTokenOnly(t *testing.T) {
	dir := t.TempDir()

	stacksPath := filepath.Join(dir, "stacks.yaml")
	stacksPayload := []byte(`
stacks:
  - name: app
    composeFile: app/docker-compose.yml
`)
	require.NoError(t, os.WriteFile(stacksPath, stacksPayload, 0o600), "write stacks file")

	tokenPath := filepath.Join(dir, "git_token")
	require.NoError(t, os.WriteFile(tokenPath, []byte("token-value"), 0o600), "write git token")

	configPath := filepath.Join(dir, "swarm-deploy.yaml")
	configPayload := []byte(fmt.Sprintf(`
git:
  repository: https://example.com/repo.git
  auth:
    type: http
    http:
      tokenPath: %s
stacks:
  file: ./stacks.yaml
`, tokenPath))
	require.NoError(t, os.WriteFile(configPath, configPayload, 0o600), "write config file")

	cfg, err := Load(configPath)
	require.NoError(t, err, "load config")
	assert.Equal(t, "oauth2", cfg.Spec.Git.Auth.HTTP.ResolveUsername(), "expected oauth2 fallback for token auth")
	assert.Equal(t, "token-value", cfg.Spec.Git.Auth.HTTP.ResolvePassword(), "expected token as password")
}
