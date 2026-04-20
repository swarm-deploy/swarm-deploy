package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/artarts36/specw"
)

const (
	SyncModePull    = "pull"
	SyncModeWebhook = "webhook"
	SyncModeHybrid  = "hybrid"

	// AuthenticationStrategyNone disables web authentication.
	AuthenticationStrategyNone = "none"
	// AuthenticationStrategyBasic enables HTTP Basic authentication.
	AuthenticationStrategyBasic = "basic"

	defaultWebAddress         = ":8080"
	defaultWebhookAddress     = ":8082"
	defaultEventHistoryCap    = 500
	defaultSyncPollInterval   = 30 * time.Second
	defaultInitJobPollEvery   = 2 * time.Second
	defaultInitJobMaxDuration = 10 * time.Minute
	defaultInitJobsTimeout    = 10 * time.Minute

	defaultAssistantOpenAIBaseURL           = "https://api.openai.com/v1"
	defaultAssistantTemperature             = "0.2"
	defaultAssistantMaxTokens               = "800"
	defaultAssistantConversationInMemoryTTL = 1 * time.Hour
)

type Config struct {
	// Spec is the runtime configuration loaded from YAML.
	Spec Spec `yaml:"-"`
}

type Spec struct {
	// DataDir is an internal working directory derived from config location.
	DataDir string `yaml:"-"`
	// Git contains repository and authentication settings.
	Git GitSpec `yaml:"git"`
	// Sync contains pull/webhook synchronization settings.
	Sync SyncSpec `yaml:"sync"`
	// StacksSource contains path to stack definitions file inside git repository.
	StacksSource StacksSourceSpec `yaml:"stacks"`
	// Stacks is a parsed list of stack specifications loaded from stacks.file.
	Stacks []StackSpec `yaml:"-"`
	// Notifications contains notification channel configuration.
	Notifications NotificationSpec `yaml:"notifications"`
	// Web contains public HTTP server settings.
	Web WebSpec `yaml:"web"`
	// HealthServer contains health and metrics server settings.
	HealthServer HealthServerSpec `yaml:"healthServer"`
	// Swarm contains docker stack deploy execution settings.
	Swarm SwarmSpec `yaml:"swarm"`
	// SecretRotation controls secret/config name rotation strategy.
	SecretRotation SecretRotationSpec `yaml:"secretRotation"`
	// EventHistory controls persisted event history settings.
	EventHistory EventHistorySpec `yaml:"eventHistory"`
	// Assistant contains AI assistant settings.
	Assistant AssistantSpec `yaml:"assistant"`
	// InitJobsTimeout is a global timeout for init jobs.
	InitJobsTimeout specw.Duration `yaml:"initJobsTimeout"`
	// Log contains level settings.
	Log struct {
		// Level for write logs. Default: INFO
		Level specw.SlogLevel `yaml:"level,omitempty"`
	} `yaml:"log"`
}

type EventHistorySpec struct {
	// Capacity is a maximum number of events to keep in history.
	Capacity int `yaml:"capacity"`
}

type SyncSpec struct {
	// Mode is sync mode: pull, webhook, or hybrid.
	Mode string `yaml:"mode"`
	// PollInterval is an interval between git pull attempts.
	PollInterval specw.Duration `yaml:"pollInterval"`
	// Webhook contains webhook sync trigger settings.
	Webhook WebhookSpec `yaml:"webhook"`
}

type WebhookSpec struct {
	// Enabled toggles webhook trigger processing.
	Enabled bool `yaml:"enabled"`
	// Address is an HTTP listen address for webhook server.
	Address string `yaml:"address"`
	// Path is an HTTP path for webhook endpoint.
	Path string `yaml:"path"`
	// Secret is a path to file containing webhook shared secret.
	Secret specw.File `yaml:"secretPath"`
}

type StacksSourceSpec struct {
	// File is a path to YAML file with stack definitions relative to repository root.
	File string `yaml:"file"`
}

type StackSpec struct {
	// Name is a Docker Swarm stack name.
	Name string `yaml:"name"`
	// ComposeFile is a path to stack compose file relative to repo root.
	ComposeFile string `yaml:"composeFile"`
}

type HealthServerSpec struct {
	// Address is an HTTP listen address for health/metrics server.
	Address string `yaml:"address"`
	// Metrics contains Prometheus endpoint settings.
	Metrics EndpointSpec `yaml:"metrics"`
	// Healthz contains healthcheck endpoint settings.
	Healthz EndpointSpec `yaml:"healthz"`
}

type EndpointSpec struct {
	// Path is an HTTP route path for endpoint.
	Path string `yaml:"path"`
}

type SwarmSpec struct {
	// Command is executable used to invoke Docker CLI.
	Command string `yaml:"command"`
	// StackDeployArgs is argument list for docker stack deploy command.
	StackDeployArgs []string `yaml:"stackDeployArgs"`
	// InitJobPollEvery is polling interval for init jobs.
	InitJobPollEvery specw.Duration `yaml:"initJobPollEvery"`
	// InitJobMaxDuration is maximum execution time for init jobs.
	InitJobMaxDuration specw.Duration `yaml:"initJobMaxDuration"`
}

type SecretRotationSpec struct {
	// Enabled toggles secret/config name rotation.
	Enabled bool `yaml:"enabled"`
	// HashLength is a length of generated hash suffix.
	HashLength int `yaml:"hashLength"`
	// IncludePath adds source path into hash input.
	IncludePath bool `yaml:"includePath"`
}

func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	var spec Spec
	if err := node.Decode(&spec); err != nil {
		return err
	}
	c.Spec = spec
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := &Config{}
	err = yaml.Unmarshal(data, cfg)
	if err != nil {
		return nil, fmt.Errorf("decode config yaml: %w", err)
	}

	configDir := filepath.Dir(path)

	err = cfg.applyDefaults(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.loadStacks(configDir)
	if err != nil {
		return nil, err
	}
	err = cfg.validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults(configDir string) error {
	c.Spec.DataDir = filepath.Join(configDir, ".swarm-deploy")
	c.applyGitAndSyncDefaults()
	c.applyWebAndHealthDefaults()
	c.Spec.Notifications.applyDefaults()
	c.applyAssistantDefaults()
	c.applySwarmDefaults()
	c.applySecretRotationDefaults()
	c.applyEventHistoryDefaults()

	if err := os.MkdirAll(c.Spec.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", c.Spec.DataDir, err)
	}

	return nil
}

func (c *Config) applyGitAndSyncDefaults() {
	if c.Spec.Git.Branch == "" {
		c.Spec.Git.Branch = "main"
	}
	if c.Spec.Sync.Mode == "" {
		c.Spec.Sync.Mode = SyncModeHybrid
	}
	if c.Spec.Sync.PollInterval.Value <= 0 {
		c.Spec.Sync.PollInterval.Value = defaultSyncPollInterval
	}
	if c.Spec.Sync.Webhook.Path == "" {
		c.Spec.Sync.Webhook.Path = "/api/v1/webhooks/git"
	}
	if strings.TrimSpace(c.Spec.Sync.Webhook.Address) == "" {
		c.Spec.Sync.Webhook.Address = defaultWebhookAddress
	}
	if c.Spec.Sync.Mode == SyncModeWebhook && !c.Spec.Sync.Webhook.Enabled {
		c.Spec.Sync.Webhook.Enabled = true
	}
}

func (c *Config) applyWebAndHealthDefaults() {
	if strings.TrimSpace(c.Spec.Web.Address) == "" {
		c.Spec.Web.Address = defaultWebAddress
	}
	if c.Spec.HealthServer.Address == "" {
		c.Spec.HealthServer.Address = ":8081"
	}
	if c.Spec.HealthServer.Metrics.Path == "" {
		c.Spec.HealthServer.Metrics.Path = "/metrics"
	}
	if c.Spec.HealthServer.Healthz.Path == "" {
		c.Spec.HealthServer.Healthz.Path = "/healthz"
	}
}

func (c *Config) applyAssistantDefaults() {
	c.Spec.Assistant.SystemPrompt = strings.TrimSpace(c.Spec.Assistant.SystemPrompt)
	c.Spec.Assistant.Model.Name = strings.TrimSpace(c.Spec.Assistant.Model.Name)
	c.Spec.Assistant.Model.EmbeddingName = strings.TrimSpace(c.Spec.Assistant.Model.EmbeddingName)
	if c.Spec.Assistant.Model.EmbeddingName == "" {
		c.Spec.Assistant.Model.EmbeddingName = c.Spec.Assistant.Model.Name
	}

	for i, tool := range c.Spec.Assistant.Tools {
		c.Spec.Assistant.Tools[i] = strings.TrimSpace(tool)
	}

	openaiCfg := &c.Spec.Assistant.Model.OpenAI
	openaiCfg.BaseURL = strings.TrimSpace(openaiCfg.BaseURL)
	if openaiCfg.BaseURL == "" {
		openaiCfg.BaseURL = defaultAssistantOpenAIBaseURL
	}

	openaiCfg.OrganizationID = strings.TrimSpace(openaiCfg.OrganizationID)

	openaiCfg.Temperature = strings.TrimSpace(openaiCfg.Temperature)
	if openaiCfg.Temperature == "" {
		openaiCfg.Temperature = defaultAssistantTemperature
	}

	openaiCfg.MaxTokens = strings.TrimSpace(openaiCfg.MaxTokens)
	if openaiCfg.MaxTokens == "" {
		openaiCfg.MaxTokens = defaultAssistantMaxTokens
	}

	inMemoryStorageCfg := &c.Spec.Assistant.Conversation.Storage.InMemory
	if inMemoryStorageCfg.TTL.Value <= 0 {
		inMemoryStorageCfg.TTL.Value = defaultAssistantConversationInMemoryTTL
	}
}

func (c *Config) applySwarmDefaults() {
	if c.Spec.Swarm.Command == "" {
		c.Spec.Swarm.Command = "docker"
	}
	if len(c.Spec.Swarm.StackDeployArgs) == 0 {
		c.Spec.Swarm.StackDeployArgs = []string{"stack", "deploy", "--with-registry-auth", "--prune"}
	}
	if c.Spec.Swarm.InitJobPollEvery.Value <= 0 {
		c.Spec.Swarm.InitJobPollEvery.Value = defaultInitJobPollEvery
	}
	if c.Spec.Swarm.InitJobMaxDuration.Value <= 0 {
		c.Spec.Swarm.InitJobMaxDuration.Value = defaultInitJobMaxDuration
	}
	if c.Spec.InitJobsTimeout.Value <= 0 {
		c.Spec.InitJobsTimeout.Value = defaultInitJobsTimeout
	}
}

func (c *Config) applySecretRotationDefaults() {
	if c.Spec.SecretRotation.HashLength <= 0 {
		c.Spec.SecretRotation.HashLength = 8
	}
}

func (c *Config) applyEventHistoryDefaults() {
	if c.Spec.EventHistory.Capacity <= 0 {
		c.Spec.EventHistory.Capacity = defaultEventHistoryCap
	}
}

func (c *Config) loadStacks(configDir string) error {
	_, err := c.ReloadStacks(filepath.Join(c.Spec.DataDir, "repo"), configDir)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReloadStacks reloads stack definitions from the first existing base directory.
// If stacks.file is absolute, the absolute path is used directly.
func (c *Config) ReloadStacks(baseDirs ...string) (string, error) {
	stacksPath, err := c.resolveStacksPath(baseDirs...)
	if err != nil {
		return "", err
	}

	stacks, err := loadStacksFromFile(stacksPath)
	if err != nil {
		return "", err
	}
	if errs := validateStacksList(stacks); len(errs) > 0 {
		return "", errors.Join(errs...)
	}

	c.Spec.Stacks = stacks
	return stacksPath, nil
}

func (c *Config) resolveStacksPath(baseDirs ...string) (string, error) {
	if c.Spec.StacksSource.File == "" {
		return "", errors.New("stacks.file is required")
	}

	if filepath.IsAbs(c.Spec.StacksSource.File) {
		return c.Spec.StacksSource.File, nil
	}

	var candidates []string
	for _, baseDir := range baseDirs {
		if strings.TrimSpace(baseDir) == "" {
			continue
		}

		candidate := filepath.Join(baseDir, c.Spec.StacksSource.File)
		candidates = append(candidates, candidate)

		_, err := os.Stat(candidate)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat stacks file %s: %w", candidate, err)
		}
	}

	if len(candidates) == 0 {
		return "", errors.New("stacks.file is relative and no baseDirs provided")
	}

	return "", fmt.Errorf(
		"stacks file %s not found in any base dir: %s: %w",
		c.Spec.StacksSource.File,
		strings.Join(candidates, ", "),
		os.ErrNotExist,
	)
}

func loadStacksFromFile(stacksPath string) ([]StackSpec, error) {
	data, err := os.ReadFile(stacksPath)
	if err != nil {
		return nil, fmt.Errorf("read stacks file %s: %w", stacksPath, err)
	}

	type stacksContainer struct {
		Stacks []StackSpec `yaml:"stacks"`
	}

	var container stacksContainer
	err = yaml.Unmarshal(data, &container)
	if err != nil {
		return nil, fmt.Errorf("decode stacks file %s: %w", stacksPath, err)
	}
	if len(container.Stacks) > 0 {
		return container.Stacks, nil
	}

	var list []StackSpec
	err = yaml.Unmarshal(data, &list)
	if err != nil {
		return nil, fmt.Errorf("decode stacks list %s: %w", stacksPath, err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("stacks file %s does not contain any stacks", stacksPath)
	}

	return list, nil
}

func (c *Config) validate() error {
	var errs []error

	errs = append(errs, c.validateRequired()...)
	errs = append(errs, c.validateStacks()...)
	errs = append(errs, c.validateSync()...)
	errs = append(errs, c.validateGitAuth()...)
	errs = append(errs, c.validateSecurity()...)
	errs = append(errs, c.Spec.Notifications.validate()...)
	errs = append(errs, c.validateAssistant()...)

	return errors.Join(errs...)
}

func (c *Config) validateRequired() []error {
	var errs []error

	if c.Spec.Git.Repository == "" {
		errs = append(errs, errors.New("git.repository is required"))
	}
	if c.Spec.StacksSource.File == "" {
		errs = append(errs, errors.New("stacks.file is required"))
	}

	return errs
}

func (c *Config) validateStacks() []error {
	if len(c.Spec.Stacks) == 0 {
		return nil
	}

	return validateStacksList(c.Spec.Stacks)
}

func validateStacksList(stacks []StackSpec) []error {
	var errs []error

	seen := map[string]struct{}{}
	for i, stack := range stacks {
		if stack.Name == "" {
			errs = append(errs, fmt.Errorf("stacks.file[%d].name is required", i))
		}
		if stack.ComposeFile == "" {
			errs = append(errs, fmt.Errorf("stacks.file[%d].composeFile is required", i))
		}
		if _, exists := seen[stack.Name]; exists {
			errs = append(errs, fmt.Errorf("stacks.file has duplicated name %q", stack.Name))
		}
		seen[stack.Name] = struct{}{}
	}

	return errs
}

func (c *Config) validateSync() []error {
	var errs []error

	switch c.Spec.Sync.Mode {
	case SyncModePull, SyncModeWebhook, SyncModeHybrid:
	default:
		errs = append(errs, fmt.Errorf("sync.mode must be one of %q|%q|%q", SyncModePull, SyncModeWebhook, SyncModeHybrid))
	}

	if c.Spec.Sync.Webhook.Enabled && c.Spec.Sync.Mode == SyncModePull {
		errs = append(errs, errors.New("sync.webhook.enabled=true conflicts with sync.mode=pull"))
	}
	errs = append(errs, c.validateWebhookSecret()...)

	return errs
}

func (c *Config) validateGitAuth() []error {
	var errs []error

	authType := c.Spec.Git.Auth.Type
	if !authType.IsSupported() {
		errs = append(errs, fmt.Errorf("git.auth.type must be one of none|http|ssh, got %q", c.Spec.Git.Auth.Type))
	}

	if authType == GitAuthTypeHTTP {
		username := strings.TrimSpace(c.Spec.Git.Auth.HTTP.Username)
		password := strings.TrimSpace(string(c.Spec.Git.Auth.HTTP.Password.Content))
		token := strings.TrimSpace(string(c.Spec.Git.Auth.HTTP.Token.Content))

		if token != "" && password != "" {
			errs = append(errs, errors.New("git.auth.http.tokenPath and git.auth.http.passwordPath are mutually exclusive"))
		}
		if token == "" && (username == "" || password == "") {
			errs = append(
				errs,
				errors.New("git.auth.http requires username+passwordPath or tokenPath"),
			)
		}
	}

	return errs
}

func (c *Config) validateSecurity() []error {
	if c.Spec.Web.Security.Authentication.Basic.HTPasswdFile.Path == "" {
		return nil
	}

	if strings.TrimSpace(string(c.Spec.Web.Security.Authentication.Basic.HTPasswdFile.Content)) == "" {
		return []error{errors.New("web.security.authentication.basic.htpasswdFile contains empty credentials")}
	}

	return nil
}

func (c *Config) validateWebhookSecret() []error {
	if !c.Spec.Sync.Webhook.Enabled {
		return nil
	}

	if strings.TrimSpace(string(c.Spec.Sync.Webhook.Secret.Content)) == "" {
		return []error{errors.New("webhook enabled but sync.webhook.secretPath contains empty secret")}
	}

	return nil
}

func (c *Config) validateAssistant() []error {
	if !c.Spec.Assistant.Enabled {
		return nil
	}

	var errs []error
	if strings.TrimSpace(c.Spec.Assistant.Model.Name) == "" {
		errs = append(errs, errors.New("assistant.model.name is required when assistant.enabled=true"))
	}

	token := c.Spec.Assistant.Model.OpenAI.APIToken.Content
	if len(token) == 0 {
		errs = append(errs, errors.New("assistant.model.openai.apiTokenPath is required when assistant.enabled=true"))
	}

	temperature, err := c.Spec.Assistant.Model.OpenAI.ResolveTemperature()
	if err != nil {
		errs = append(errs, fmt.Errorf("assistant.model.openai.temperature %w", err))
	} else if temperature < 0 || temperature > 2 {
		errs = append(errs, errors.New("assistant.model.openai.temperature must be between 0 and 2"))
	}

	maxTokens, err := c.Spec.Assistant.Model.OpenAI.ResolveMaxTokens()
	if err != nil {
		errs = append(errs, fmt.Errorf("assistant.model.openai.maxTokens %w", err))
	} else if maxTokens <= 0 {
		errs = append(errs, errors.New("assistant.model.openai.maxTokens must be > 0"))
	}

	for i, toolName := range c.Spec.Assistant.Tools {
		if strings.TrimSpace(toolName) == "" {
			errs = append(errs, fmt.Errorf("assistant.tools[%d] must not be empty", i))
		}
	}

	return errs
}

func (a AssistantOpenAISpec) ResolveTemperature() (float64, error) {
	temperature := strings.TrimSpace(a.Temperature)
	if temperature == "" {
		return 0, errors.New("is empty")
	}

	value, err := strconv.ParseFloat(temperature, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", temperature, err)
	}

	return value, nil
}

func (a AssistantOpenAISpec) ResolveMaxTokens() (int, error) {
	maxTokens := strings.TrimSpace(a.MaxTokens)
	if maxTokens == "" {
		return 0, errors.New("is empty")
	}

	value, err := strconv.Atoi(maxTokens)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", maxTokens, err)
	}

	return value, nil
}

// Strategy resolves configured web authentication strategy.
func (a AuthenticationSpec) Strategy() string {
	if strings.TrimSpace(a.Basic.HTPasswdFile.Path) != "" {
		return AuthenticationStrategyBasic
	}

	return AuthenticationStrategyNone
}
