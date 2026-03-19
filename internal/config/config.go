package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/artarts36/specw"
)

const (
	SyncModePull    = "pull"
	SyncModeWebhook = "webhook"
	SyncModeHybrid  = "hybrid"

	defaultSyncPollInterval   = 30 * time.Second
	defaultInitJobPollEvery   = 2 * time.Second
	defaultInitJobMaxDuration = 10 * time.Minute
	defaultInitJobsTimeout    = 10 * time.Minute
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
	// StacksFile points to a YAML file with stack definitions.
	StacksFile string `yaml:"stacksFile"`
	// Stacks is a parsed list of stack specifications loaded from StacksFile.
	Stacks []StackSpec `yaml:"-"`
	// Notifications contains notification channel configuration.
	Notifications NotificationSpec `yaml:"notifications"`
	// Web contains API and frontend HTTP server addresses.
	Web WebSpec `yaml:"web"`
	// HealthServer contains health and metrics server settings.
	HealthServer HealthServerSpec `yaml:"healthServer"`
	// Swarm contains docker stack deploy execution settings.
	Swarm SwarmSpec `yaml:"swarm"`
	// SecretRotation controls secret/config name rotation strategy.
	SecretRotation SecretRotationSpec `yaml:"secretRotation"`
	// InitJobsTimeout is a global timeout for init jobs.
	InitJobsTimeout specw.Duration `yaml:"initJobsTimeout"`
}

type GitSpec struct {
	// Repository is a git repository URL (ssh or https).
	Repository string `yaml:"repository"`
	// Branch is a git branch to track.
	Branch string `yaml:"branch"`
	// Auth contains git authentication settings.
	Auth GitAuthSpec `yaml:"auth"`
}

type GitAuthSpec struct {
	// Type is git auth type: none, http, or ssh.
	Type string `yaml:"type"`
	// HTTP is HTTP(S) basic/token authentication configuration.
	HTTP GitHTTPAuth `yaml:"http"`
	// SSH is SSH authentication configuration.
	SSH GitSSHAuthSpec `yaml:"ssh"`
}

type GitHTTPAuth struct {
	// Username is HTTP basic auth username.
	Username string `yaml:"username"`
	// Password is HTTP basic auth password.
	//nolint:gosec // Field name is part of a user-facing config schema and does not imply hardcoded secret usage.
	Password string `yaml:"password"`
	// PasswordEnv is an env variable name containing HTTP password.
	PasswordEnv string `yaml:"passwordEnv"`
	// Token is an HTTP token value used as password.
	Token string `yaml:"token"`
	// TokenEnv is an env variable name containing HTTP token.
	TokenEnv string `yaml:"tokenEnv"`
}

type GitSSHAuthSpec struct {
	// User is an SSH user, typically "git".
	User string `yaml:"user"`
	// PrivateKeyPath is a path to a private key file for git SSH auth.
	PrivateKeyPath string `yaml:"privateKeyPath"`
	// KnownHostsPath is a path to known_hosts file used for host verification.
	KnownHostsPath string `yaml:"knownHostsPath"`
	// InsecureIgnoreHostKey disables SSH host key verification.
	InsecureIgnoreHostKey bool `yaml:"insecureIgnoreHostKey"`
	// PassphrasePath is a path to file containing private key passphrase.
	PassphrasePath string `yaml:"passphrasePath"`
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
	// SecretPath is a path to file containing webhook shared secret.
	SecretPath string `yaml:"secretPath"`
}

type StackSpec struct {
	// Name is a Docker Swarm stack name.
	Name string `yaml:"name"`
	// ComposeFile is a path to stack compose file relative to repo root.
	ComposeFile string `yaml:"composeFile"`
}

type NotificationSpec struct {
	// Telegram is a list of Telegram notification channels.
	Telegram []TelegramChannel `yaml:"telegram"`
	// Custom is a list of custom webhook notification channels.
	Custom []CustomChannel `yaml:"custom"`
}

type TelegramChannel struct {
	// Name is a logical channel name used in logs/diagnostics.
	Name string `yaml:"name"`
	// BotTokenPath is a path to file containing Telegram bot token.
	BotTokenPath string `yaml:"botTokenPath"`
	// ChatID is a target Telegram chat identifier.
	ChatID string `yaml:"chatId"`
	// ChatThreadID is an optional topic/thread id inside target chat.
	ChatThreadID int64 `yaml:"chatThreadId"`
	// Message is a text/template used for notification rendering.
	Message string `yaml:"message"`
}

type CustomChannel struct {
	// Name is a logical channel name used in logs/diagnostics.
	Name string `yaml:"name"`
	// URL is a webhook endpoint URL.
	URL string `yaml:"url"`
	// URLEnv is an env variable name containing webhook URL.
	URLEnv string `yaml:"urlEnv"`
	// Method is an HTTP method for webhook delivery.
	Method string `yaml:"method"`
	// Header contains additional HTTP headers for webhook delivery.
	Header map[string]string `yaml:"header"`
}

type WebSpec struct {
	// APIAddress is an HTTP listen address for API server.
	APIAddress string `yaml:"apiAddress"`
	// FrontendAddress is an HTTP listen address for frontend server.
	FrontendAddress string `yaml:"frontendAddress"`
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
	c.applyGitAndSyncDefaults(configDir)
	c.applyWebAndHealthDefaults()
	c.applyNotificationDefaults(configDir)
	c.applySwarmDefaults()
	c.applySecretRotationDefaults()

	if err := os.MkdirAll(c.Spec.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", c.Spec.DataDir, err)
	}

	return nil
}

func (c *Config) applyGitAndSyncDefaults(configDir string) {
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
		c.Spec.Sync.Webhook.Address = ":8080"
	}
	if secretPath := strings.TrimSpace(c.Spec.Sync.Webhook.SecretPath); secretPath != "" &&
		!filepath.IsAbs(secretPath) {
		c.Spec.Sync.Webhook.SecretPath = filepath.Join(configDir, secretPath)
	}
	if passphrasePath := strings.TrimSpace(c.Spec.Git.Auth.SSH.PassphrasePath); passphrasePath != "" &&
		!filepath.IsAbs(passphrasePath) {
		c.Spec.Git.Auth.SSH.PassphrasePath = filepath.Join(configDir, passphrasePath)
	}
	if c.Spec.Sync.Mode == SyncModeWebhook && !c.Spec.Sync.Webhook.Enabled {
		c.Spec.Sync.Webhook.Enabled = true
	}
}

func (c *Config) applyWebAndHealthDefaults() {
	if strings.TrimSpace(c.Spec.Web.APIAddress) == "" {
		c.Spec.Web.APIAddress = ":8080"
	}
	if strings.TrimSpace(c.Spec.Web.FrontendAddress) == "" {
		c.Spec.Web.FrontendAddress = ":8080"
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

func (c *Config) applyNotificationDefaults(configDir string) {
	for i := range c.Spec.Notifications.Telegram {
		tokenPath := strings.TrimSpace(c.Spec.Notifications.Telegram[i].BotTokenPath)
		if tokenPath != "" && !filepath.IsAbs(tokenPath) {
			c.Spec.Notifications.Telegram[i].BotTokenPath = filepath.Join(configDir, tokenPath)
		}
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

func (c *Config) loadStacks(configDir string) error {
	if c.Spec.StacksFile == "" {
		return errors.New("stacksFile is required")
	}

	stacksPath := c.Spec.StacksFile
	if !filepath.IsAbs(stacksPath) {
		stacksPath = filepath.Join(configDir, stacksPath)
	}

	data, err := os.ReadFile(stacksPath)
	if err != nil {
		return fmt.Errorf("read stacks file %s: %w", stacksPath, err)
	}

	type stacksContainer struct {
		Stacks []StackSpec `yaml:"stacks"`
	}

	var container stacksContainer
	err = yaml.Unmarshal(data, &container)
	if err != nil {
		return fmt.Errorf("decode stacks file %s: %w", stacksPath, err)
	}
	if len(container.Stacks) > 0 {
		c.Spec.Stacks = container.Stacks
		return nil
	}

	var list []StackSpec
	err = yaml.Unmarshal(data, &list)
	if err != nil {
		return fmt.Errorf("decode stacks list %s: %w", stacksPath, err)
	}
	if len(list) == 0 {
		return fmt.Errorf("stacks file %s does not contain any stacks", stacksPath)
	}

	c.Spec.Stacks = list
	return nil
}

func (c *Config) validate() error {
	var errs []error

	errs = append(errs, c.validateRequired()...)
	errs = append(errs, c.validateStacks()...)
	errs = append(errs, c.validateSync()...)
	errs = append(errs, c.validateGitAuth()...)
	errs = append(errs, c.validateTelegramNotifications()...)
	errs = append(errs, c.validateCustomNotifications()...)

	return errors.Join(errs...)
}

func (c *Config) validateRequired() []error {
	var errs []error

	if c.Spec.Git.Repository == "" {
		errs = append(errs, errors.New("git.repository is required"))
	}
	if c.Spec.StacksFile == "" {
		errs = append(errs, errors.New("stacksFile is required"))
	}
	if len(c.Spec.Stacks) == 0 {
		errs = append(errs, errors.New("stacks file must contain at least one stack"))
	}

	return errs
}

func (c *Config) validateStacks() []error {
	var errs []error

	seen := map[string]struct{}{}
	for i, stack := range c.Spec.Stacks {
		if stack.Name == "" {
			errs = append(errs, fmt.Errorf("stacksFile[%d].name is required", i))
		}
		if stack.ComposeFile == "" {
			errs = append(errs, fmt.Errorf("stacksFile[%d].composeFile is required", i))
		}
		if _, exists := seen[stack.Name]; exists {
			errs = append(errs, fmt.Errorf("stacksFile has duplicated name %q", stack.Name))
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

	authType := strings.ToLower(strings.TrimSpace(c.Spec.Git.Auth.Type))
	switch authType {
	case "", "none", "http", "ssh":
	default:
		errs = append(errs, fmt.Errorf("git.auth.type must be one of none|http|ssh, got %q", c.Spec.Git.Auth.Type))
	}

	return errs
}

func (c *Config) validateWebhookSecret() []error {
	if !c.Spec.Sync.Webhook.Enabled {
		return nil
	}

	secretPath := strings.TrimSpace(c.Spec.Sync.Webhook.SecretPath)
	if secretPath == "" {
		return []error{errors.New("webhook enabled but sync.webhook.secretPath is empty")}
	}

	payload, err := os.ReadFile(secretPath)
	if err != nil {
		return []error{fmt.Errorf("read sync.webhook.secretPath %s: %w", secretPath, err)}
	}
	if strings.TrimSpace(string(payload)) == "" {
		return []error{errors.New("webhook enabled but sync.webhook.secretPath contains empty secret")}
	}

	return nil
}

func (c *Config) validateTelegramNotifications() []error {
	var errs []error

	for i, tg := range c.Spec.Notifications.Telegram {
		if tg.ChatID == "" {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].chatId is required", i))
		}
		token, err := tg.ResolveToken()
		if err != nil {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d]: %w", i, err))
		} else if token == "" {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].botTokenPath contains empty token", i))
		}
		if tg.ResolveChatThreadID() < 0 {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].chatThreadId must be >= 0", i))
		}
	}

	return errs
}

func (c *Config) validateCustomNotifications() []error {
	var errs []error

	for i, ch := range c.Spec.Notifications.Custom {
		if ch.ResolveURL() == "" {
			errs = append(errs, fmt.Errorf("notifications.custom[%d].url or urlEnv is required", i))
		}
	}

	return errs
}

func (w WebhookSpec) ResolveSecret() string {
	secretPath := strings.TrimSpace(w.SecretPath)
	if secretPath == "" {
		return ""
	}
	payload, err := os.ReadFile(secretPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(payload))
}

func (c *Config) WebhookSecret() string {
	return c.Spec.Sync.Webhook.ResolveSecret()
}

func (a GitHTTPAuth) ResolvePassword() string {
	if a.PasswordEnv != "" {
		return os.Getenv(a.PasswordEnv)
	}
	if a.TokenEnv != "" {
		return os.Getenv(a.TokenEnv)
	}
	if a.Token != "" {
		return a.Token
	}
	return a.Password
}

func (a GitHTTPAuth) ResolveUsername() string {
	if a.Username != "" {
		return a.Username
	}
	if a.Token != "" || a.TokenEnv != "" {
		// go-git basic auth requires non-empty username when token is used.
		return "oauth2"
	}
	return ""
}

func (a GitSSHAuthSpec) ResolvePassphrase() (string, error) {
	passphrasePath := strings.TrimSpace(a.PassphrasePath)
	if passphrasePath == "" {
		return "", nil
	}

	payload, err := os.ReadFile(passphrasePath)
	if err != nil {
		return "", fmt.Errorf("read passphrasePath %s: %w", passphrasePath, err)
	}

	return strings.TrimSpace(string(payload)), nil
}

func (t TelegramChannel) ResolveToken() (string, error) {
	tokenPath := strings.TrimSpace(t.BotTokenPath)
	if tokenPath == "" {
		return "", errors.New("botTokenPath is required")
	}

	payload, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("read botTokenPath %s: %w", tokenPath, err)
	}

	return strings.TrimSpace(string(payload)), nil
}

func (t TelegramChannel) ResolveChatThreadID() int64 {
	return t.ChatThreadID
}

func (c CustomChannel) ResolveURL() string {
	if c.URLEnv != "" {
		return os.Getenv(c.URLEnv)
	}
	return c.URL
}
