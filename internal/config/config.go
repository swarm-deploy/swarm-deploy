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
)

type Config struct {
	Spec Spec `yaml:"-"`
}

type Spec struct {
	DataDir         string             `yaml:"-"`
	Git             GitSpec            `yaml:"git"`
	Sync            SyncSpec           `yaml:"sync"`
	StacksFile      string             `yaml:"stacksFile"`
	Stacks          []StackSpec        `yaml:"-"`
	Notifications   NotificationSpec   `yaml:"notifications"`
	Web             WebSpec            `yaml:"web"`
	HealthServer    HealthServerSpec   `yaml:"healthServer"`
	Swarm           SwarmSpec          `yaml:"swarm"`
	SecretRotation  SecretRotationSpec `yaml:"secretRotation"`
	InitJobsTimeout specw.Duration     `yaml:"initJobsTimeout"`
}

type GitSpec struct {
	Repository string      `yaml:"repository"`
	Branch     string      `yaml:"branch"`
	Path       string      `yaml:"path"`
	Auth       GitAuthSpec `yaml:"auth"`
}

type GitAuthSpec struct {
	Type string         `yaml:"type"`
	HTTP GitHTTPAuth    `yaml:"http"`
	SSH  GitSSHAuthSpec `yaml:"ssh"`
}

type GitHTTPAuth struct {
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	PasswordEnv string `yaml:"passwordEnv"`
	Token       string `yaml:"token"`
	TokenEnv    string `yaml:"tokenEnv"`
}

type GitSSHAuthSpec struct {
	User                  string `yaml:"user"`
	PrivateKeyPath        string `yaml:"privateKeyPath"`
	PrivateKey            string `yaml:"privateKey"`
	PrivateKeyEnv         string `yaml:"privateKeyEnv"`
	KnownHostsPath        string `yaml:"knownHostsPath"`
	InsecureIgnoreHostKey bool   `yaml:"insecureIgnoreHostKey"`
	PassphraseEnv         string `yaml:"passphraseEnv"`
}

type SyncSpec struct {
	Mode         string         `yaml:"mode"`
	PollInterval specw.Duration `yaml:"pollInterval"`
	Webhook      WebhookSpec    `yaml:"webhook"`
}

type WebhookSpec struct {
	Enabled    bool   `yaml:"enabled"`
	Path       string `yaml:"path"`
	SecretFile string `yaml:"secretFile"`
}

type StackSpec struct {
	Name        string `yaml:"name"`
	ComposeFile string `yaml:"composeFile"`
}

type NotificationSpec struct {
	Telegram []TelegramChannel `yaml:"telegram"`
	Custom   []CustomChannel   `yaml:"custom"`
}

type TelegramChannel struct {
	Name               string `yaml:"name"`
	BotTokenSecretFile string `yaml:"botTokenSecretFile"`
	ChatID             string `yaml:"chatId"`
	ChatThreadID       int64  `yaml:"chatThreadId"`
	Message            string `yaml:"message"`
}

type CustomChannel struct {
	Name   string            `yaml:"name"`
	URL    string            `yaml:"url"`
	URLEnv string            `yaml:"urlEnv"`
	Method string            `yaml:"method"`
	Header map[string]string `yaml:"header"`
}

type WebSpec struct {
	Address string `yaml:"address"`
}

type HealthServerSpec struct {
	Address string       `yaml:"address"`
	Metrics EndpointSpec `yaml:"metrics"`
	Healthz EndpointSpec `yaml:"healthz"`
}

type EndpointSpec struct {
	Enabled *bool  `yaml:"enabled"`
	Path    string `yaml:"path"`
}

func (e EndpointSpec) EnabledOrDefault(defaultValue bool) bool {
	if e.Enabled == nil {
		return defaultValue
	}
	return *e.Enabled
}

type SwarmSpec struct {
	Command            string         `yaml:"command"`
	StackDeployArgs    []string       `yaml:"stackDeployArgs"`
	InitJobPollEvery   specw.Duration `yaml:"initJobPollEvery"`
	InitJobMaxDuration specw.Duration `yaml:"initJobMaxDuration"`
}

type SecretRotationSpec struct {
	Enabled     bool `yaml:"enabled"`
	HashLength  int  `yaml:"hashLength"`
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
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("decode config yaml: %w", err)
	}

	configDir := filepath.Dir(path)

	if err := cfg.applyDefaults(configDir); err != nil {
		return nil, err
	}
	if err := cfg.loadStacks(configDir); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults(configDir string) error {
	c.Spec.DataDir = filepath.Join(configDir, ".swarm-deploy")

	if c.Spec.Git.Branch == "" {
		c.Spec.Git.Branch = "main"
	}
	if c.Spec.Sync.Mode == "" {
		c.Spec.Sync.Mode = SyncModeHybrid
	}
	if c.Spec.Sync.PollInterval.Value <= 0 {
		c.Spec.Sync.PollInterval.Value = 30 * time.Second
	}
	if c.Spec.Sync.Webhook.Path == "" {
		c.Spec.Sync.Webhook.Path = "/api/v1/webhooks/git"
	}
	if secretFile := strings.TrimSpace(c.Spec.Sync.Webhook.SecretFile); secretFile != "" && !filepath.IsAbs(secretFile) {
		c.Spec.Sync.Webhook.SecretFile = filepath.Join(configDir, secretFile)
	}
	if c.Spec.Sync.Mode == SyncModeWebhook && !c.Spec.Sync.Webhook.Enabled {
		c.Spec.Sync.Webhook.Enabled = true
	}

	if c.Spec.Web.Address == "" {
		c.Spec.Web.Address = ":8080"
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
	if c.Spec.HealthServer.Healthz.Enabled == nil {
		c.Spec.HealthServer.Healthz.Enabled = boolPtr(true)
	}

	for i := range c.Spec.Notifications.Telegram {
		secretFile := strings.TrimSpace(c.Spec.Notifications.Telegram[i].BotTokenSecretFile)
		if secretFile != "" && !filepath.IsAbs(secretFile) {
			c.Spec.Notifications.Telegram[i].BotTokenSecretFile = filepath.Join(configDir, secretFile)
		}
	}

	if c.Spec.Swarm.Command == "" {
		c.Spec.Swarm.Command = "docker"
	}
	if len(c.Spec.Swarm.StackDeployArgs) == 0 {
		c.Spec.Swarm.StackDeployArgs = []string{"stack", "deploy", "--with-registry-auth", "--prune"}
	}
	if c.Spec.Swarm.InitJobPollEvery.Value <= 0 {
		c.Spec.Swarm.InitJobPollEvery.Value = 2 * time.Second
	}
	if c.Spec.Swarm.InitJobMaxDuration.Value <= 0 {
		c.Spec.Swarm.InitJobMaxDuration.Value = 10 * time.Minute
	}
	if c.Spec.InitJobsTimeout.Value <= 0 {
		c.Spec.InitJobsTimeout.Value = 10 * time.Minute
	}

	if c.Spec.SecretRotation.HashLength <= 0 {
		c.Spec.SecretRotation.HashLength = 8
	}

	if err := os.MkdirAll(c.Spec.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", c.Spec.DataDir, err)
	}

	return nil
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
	if err := yaml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("decode stacks file %s: %w", stacksPath, err)
	}
	if len(container.Stacks) > 0 {
		c.Spec.Stacks = container.Stacks
		return nil
	}

	var list []StackSpec
	if err := yaml.Unmarshal(data, &list); err != nil {
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

	if c.Spec.Git.Repository == "" {
		errs = append(errs, errors.New("git.repository is required"))
	}
	if c.Spec.StacksFile == "" {
		errs = append(errs, errors.New("stacksFile is required"))
	}
	if len(c.Spec.Stacks) == 0 {
		errs = append(errs, errors.New("stacks file must contain at least one stack"))
	}

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

	switch c.Spec.Sync.Mode {
	case SyncModePull, SyncModeWebhook, SyncModeHybrid:
	default:
		errs = append(errs, fmt.Errorf("sync.mode must be one of %q|%q|%q", SyncModePull, SyncModeWebhook, SyncModeHybrid))
	}

	authType := strings.ToLower(strings.TrimSpace(c.Spec.Git.Auth.Type))
	switch authType {
	case "", "none", "http", "ssh":
	default:
		errs = append(errs, fmt.Errorf("git.auth.type must be one of none|http|ssh, got %q", c.Spec.Git.Auth.Type))
	}

	if c.Spec.Sync.Webhook.Enabled && c.Spec.Sync.Mode == SyncModePull {
		errs = append(errs, errors.New("sync.webhook.enabled=true conflicts with sync.mode=pull"))
	}

	if c.Spec.Sync.Webhook.Enabled {
		secretFile := strings.TrimSpace(c.Spec.Sync.Webhook.SecretFile)
		if secretFile == "" {
			errs = append(errs, errors.New("webhook enabled but sync.webhook.secretFile is empty"))
		} else {
			payload, err := os.ReadFile(secretFile)
			if err != nil {
				errs = append(errs, fmt.Errorf("read sync.webhook.secretFile %s: %w", secretFile, err))
			} else if strings.TrimSpace(string(payload)) == "" {
				errs = append(errs, errors.New("webhook enabled but sync.webhook.secretFile contains empty secret"))
			}
		}
	}

	for i, tg := range c.Spec.Notifications.Telegram {
		if tg.ChatID == "" {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].chatId is required", i))
		}
		token, err := tg.ResolveToken()
		if err != nil {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d]: %w", i, err))
		} else if token == "" {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].botTokenSecretFile contains empty token", i))
		}
		if tg.ResolveChatThreadID() < 0 {
			errs = append(errs, fmt.Errorf("notifications.telegram[%d].chatThreadId must be >= 0", i))
		}
	}

	for i, ch := range c.Spec.Notifications.Custom {
		if ch.ResolveURL() == "" {
			errs = append(errs, fmt.Errorf("notifications.custom[%d].url or urlEnv is required", i))
		}
	}

	return errors.Join(errs...)
}

func (w WebhookSpec) ResolveSecret() string {
	secretFile := strings.TrimSpace(w.SecretFile)
	if secretFile == "" {
		return ""
	}
	payload, err := os.ReadFile(secretFile)
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

func (a GitSSHAuthSpec) ResolvePrivateKey() string {
	if a.PrivateKeyEnv != "" {
		return os.Getenv(a.PrivateKeyEnv)
	}
	return a.PrivateKey
}

func (a GitSSHAuthSpec) ResolvePassphrase() string {
	if a.PassphraseEnv == "" {
		return ""
	}
	return os.Getenv(a.PassphraseEnv)
}

func (t TelegramChannel) ResolveToken() (string, error) {
	tokenPath := strings.TrimSpace(t.BotTokenSecretFile)
	if tokenPath == "" {
		return "", errors.New("botTokenSecretFile is required")
	}

	payload, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("read botTokenSecretFile %s: %w", tokenPath, err)
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

func boolPtr(v bool) *bool {
	return &v
}
