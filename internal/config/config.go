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
	"github.com/artarts36/swarm-deploy/internal/event/events"
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

type NotificationSpec struct {
	// On maps event types to notification channels.
	On map[events.Type]struct {
		// Telegram is a list of Telegram notification channels.
		Telegram []TelegramChannel `yaml:"telegram"`
		// Custom is a list of custom webhook notification channels.
		Custom []CustomChannel `yaml:"custom"`
	} `yaml:"on"`
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
	// Address is an HTTP listen address for UI and API server.
	Address string `yaml:"address"`
	// Security contains UI and API access settings.
	Security SecuritySpec `yaml:"security"`
}

type SecuritySpec struct {
	// Authentication contains web authentication strategy settings.
	Authentication AuthenticationSpec `yaml:"authentication"`
}

type AuthenticationSpec struct {
	// Basic contains HTTP Basic authentication settings.
	Basic BasicAuthenticationSpec `yaml:"basic"`
}

type BasicAuthenticationSpec struct {
	// HTPasswdFile is a path to htpasswd file with user credentials.
	HTPasswdFile string `yaml:"htpasswdFile"`
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
	c.applySecurityDefaults(configDir)
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
		c.Spec.Sync.Webhook.Address = defaultWebhookAddress
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

func (c *Config) applySecurityDefaults(configDir string) {
	htpasswdPath := strings.TrimSpace(c.Spec.Web.Security.Authentication.Basic.HTPasswdFile)
	if htpasswdPath != "" && !filepath.IsAbs(htpasswdPath) {
		c.Spec.Web.Security.Authentication.Basic.HTPasswdFile = filepath.Join(configDir, htpasswdPath)
	}
}

func (c *Config) applyNotificationDefaults(configDir string) {
	for eventType, channels := range c.Spec.Notifications.On {
		for i := range channels.Telegram {
			tokenPath := strings.TrimSpace(channels.Telegram[i].BotTokenPath)
			if tokenPath != "" && !filepath.IsAbs(tokenPath) {
				channels.Telegram[i].BotTokenPath = filepath.Join(configDir, tokenPath)
			}
		}

		c.Spec.Notifications.On[eventType] = channels
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
	errs = append(errs, c.validateNotifications()...)

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

	authType := strings.ToLower(strings.TrimSpace(c.Spec.Git.Auth.Type))
	switch authType {
	case "", "none", "http", "ssh":
	default:
		errs = append(errs, fmt.Errorf("git.auth.type must be one of none|http|ssh, got %q", c.Spec.Git.Auth.Type))
	}

	return errs
}

func (c *Config) validateSecurity() []error {
	htpasswdPath := strings.TrimSpace(c.Spec.Web.Security.Authentication.Basic.HTPasswdFile)
	if htpasswdPath == "" {
		return nil
	}

	payload, err := os.ReadFile(htpasswdPath)
	if err != nil {
		return []error{fmt.Errorf("read web.security.authentication.basic.htpasswdFile %s: %w", htpasswdPath, err)}
	}
	if strings.TrimSpace(string(payload)) == "" {
		return []error{errors.New("web.security.authentication.basic.htpasswdFile contains empty credentials")}
	}

	return nil
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

func (c *Config) validateNotifications() []error {
	var errs []error

	for eventType, channels := range c.Spec.Notifications.On {
		for i, tg := range channels.Telegram {
			if tg.ChatID == "" {
				errs = append(errs, fmt.Errorf("notifications.on[%q].telegram[%d].chatId is required", eventType, i))
			}
			token, err := tg.ResolveToken()
			if err != nil {
				errs = append(errs, fmt.Errorf("notifications.on[%q].telegram[%d]: %w", eventType, i, err))
			} else if token == "" {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].telegram[%d].botTokenPath contains empty token", eventType, i),
				)
			}
			if tg.ResolveChatThreadID() < 0 {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].telegram[%d].chatThreadId must be >= 0", eventType, i),
				)
			}
		}

		for i, ch := range channels.Custom {
			if ch.ResolveURL() == "" {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].custom[%d].url or urlEnv is required", eventType, i),
				)
			}
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

// Strategy resolves configured web authentication strategy.
func (a AuthenticationSpec) Strategy() string {
	if strings.TrimSpace(a.Basic.HTPasswdFile) != "" {
		return AuthenticationStrategyBasic
	}

	return AuthenticationStrategyNone
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
