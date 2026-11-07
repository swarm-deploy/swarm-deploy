package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"go.yaml.in/yaml/v3"

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
	// AuthenticationStrategyAuthProxy enables authentication through a trusted reverse proxy.
	AuthenticationStrategyAuthProxy = "auth_proxy"

	defaultWebAddress              = ":8080"
	defaultWebBasePath             = "/"
	defaultWebhookAddress          = ":8082"
	defaultEventHistoryCap         = 500
	defaultGitPullInterval         = 30 * time.Second
	defaultSyncInterval            = 1 * time.Minute
	defaultSyncParallelism         = 1
	DefaultGitPullTimeout          = 2 * time.Minute
	defaultDeploymentPollInterval  = 2 * time.Second
	defaultInitJobMaxDuration      = 10 * time.Minute
	defaultRotationKeepLast        = 2
	defaultRotationMinAge          = time.Hour
	defaultRotationCleanupInterval = 4 * time.Hour

	defaultAssistantOpenAIBaseURL           = "https://api.openai.com/v1"
	defaultAssistantTemperature             = "0.2"
	defaultAssistantMaxTokens               = 800
	defaultAssistantConversationInMemoryTTL = 1 * time.Hour
	defaultManagedNetworkDriver             = "overlay"

	managedNetworkLabelKey   = "org.swarm-deploy.network.managed"
	managedNetworkLabelValue = "true"
)

type Config struct {
	// Spec is the runtime configuration loaded from YAML.
	Spec Spec `yaml:"-"`

	mu       sync.RWMutex
	stacks   []StackSpec
	networks []NetworkSpec
}

type Spec struct {
	// DataDir is an internal working directory derived from config location.
	DataDir string `yaml:"-"`
	// GitOps contains desired-state sources and reconciliation settings.
	GitOps GitOpsSpec `yaml:"gitops"`
	// Deployments contains deployment execution settings.
	Deployments DeploymentsSpec `yaml:"deployments"`
	// Event contains event module settings.
	Event EventSpec `yaml:"event"`
	// System contains process-wide logging and tracing settings.
	System SystemSpec `yaml:"system"`
	// Notifications contains notification channel configuration.
	Notifications NotificationSpec `yaml:"notifications"`
	// Web contains public HTTP server settings.
	Web WebSpec `yaml:"web"`
	// HealthServer contains health and metrics server settings.
	HealthServer HealthServerSpec `yaml:"healthServer"`
	// Assistant contains AI assistant settings.
	Assistant AssistantSpec `yaml:"assistant"`
	// Hostings contains shared Git hosting provider settings.
	Hostings githosting.Config `yaml:"hostings"`
}

type GitOpsSpec struct {
	// Git contains repository and authentication settings.
	Git GitSpec `yaml:"git"`
	// Sync contains reconciliation and webhook settings.
	Sync SyncSpec `yaml:"sync"`
	// Stacks contains the path to stack definitions inside the Git repository.
	Stacks StacksSourceSpec `yaml:"stacks"`
	// Networks contains the optional path to network definitions inside the Git repository.
	Networks NetworksSourceSpec `yaml:"networks"`
	// Injection configures settings injected into GitOps-managed services.
	Injection GitOpsInjectionSpec `yaml:"injection"`
	// Secrets contains settings for secrets read from Git.
	Secrets SecretsSpec `yaml:"secrets"`
	// ResourceRotation controls secret and config name rotation.
	ResourceRotation ResourceRotationSpec `yaml:"resourceRotation"`
}

type GitOpsInjectionSpec struct {
	// Downward configures injection of Swarm metadata into containers.
	Downward DownwardSpec `yaml:"downward"`
}

type DownwardSpec struct {
	// Enabled toggles Downward environment injection into services.
	Enabled bool `yaml:"enabled"`
}

type SecretsSpec struct {
	// Age configures decryption independently of name rotation.
	Age AgeSpec `yaml:"age"`
}

type SystemSpec struct {
	// Log contains log level settings.
	Log LogSpec `yaml:"log"`
	// Tracing contains OpenTelemetry tracing settings. Nil disables tracing.
	Tracing *TracingSpec `yaml:"tracing"`
}

type LogSpec struct {
	// Level sets the minimum log severity. Defaults to INFO.
	Level specw.SlogLevel `yaml:"level,omitempty"`
}

type EventSpec struct {
	// History controls persisted event history.
	History EventHistorySpec `yaml:"history"`
}

type EventHistorySpec struct {
	// Capacity is a maximum number of events to keep in history.
	Capacity int `yaml:"capacity"`
}

type SyncSpec struct {
	// Mode is sync mode: pull, webhook, or hybrid.
	Mode string `yaml:"mode"`
	// Interval is an interval between reconciliations against the current local desired state.
	Interval specw.Duration `yaml:"interval"`
	// Parallelism is the maximum number of stacks reconciled at the same time.
	Parallelism int `yaml:"parallelism"`
	// Policy contains synchronization behavior flags.
	Policy SyncPolicySpec `yaml:"policy"`
	// Webhook contains webhook sync trigger settings.
	Webhook WebhookSpec `yaml:"webhook"`
}

type SyncPolicySpec struct {
	// Prune enables deletion of orphaned managed services.
	Prune bool `yaml:"prune"`
	// SelfHeal enables automatic remediation of detected service drift.
	SelfHeal bool `yaml:"selfHeal"`
	// Image contains image reference restrictions.
	Image ImagePolicySpec `yaml:"image"`
}

// ImagePolicySpec contains image reference restrictions.
type ImagePolicySpec struct {
	// Tag contains image tag restrictions.
	Tag ImageTagPolicySpec `yaml:"tag"`
}

// Enabled reports whether at least one image policy rule is active.
func (s ImagePolicySpec) Enabled() bool {
	return s.Tag.Required || s.Tag.NoLatest || s.Tag.OnlySHA
}

// ImageTagPolicySpec contains restrictions for service image references.
type ImageTagPolicySpec struct {
	// Required requires an explicit tag or digest.
	Required bool `yaml:"required"`
	// NoLatest rejects explicit and implicit latest tags.
	NoLatest bool `yaml:"no_latest"`
	// OnlySHA requires a digest reference.
	OnlySHA bool `yaml:"only_sha"`
}

type WebhookSpec struct {
	// Enabled toggles webhook trigger processing.
	Enabled bool `yaml:"enabled"`
	// Address is an HTTP listen address for webhook server.
	Address string `yaml:"address"`
	// Path is an HTTP path for webhook endpoint.
	Path string `yaml:"path"`
	// Auth contains explicitly enabled webhook authentication methods.
	Auth []WebhookAuthSpec `yaml:"auth"`
	// RateLimit controls the global webhook request rate.
	RateLimit WebhookRateLimitSpec `yaml:"rateLimit"`
	// MaxBodyBytes limits the request body size read by the webhook endpoint.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
}

type StacksSourceSpec struct {
	// File is a path to YAML file with stack definitions relative to repository root.
	File string `yaml:"file"`
}

type NetworksSourceSpec struct {
	// File is a path to network definitions file inside git repository.
	File string `yaml:"file"`
}

type StackSpec struct {
	// Name is a Docker Swarm stack name.
	Name string `yaml:"name"`
	// ComposeFile is a path to stack compose file relative to repo root.
	ComposeFile string `yaml:"composeFile"`
	// Sync contains stack-specific synchronization options.
	Sync StackSyncSpec `yaml:"sync"`
}

type StackSyncSpec struct {
	// Policy contains stack-specific synchronization behavior flags.
	Policy StackSyncPolicySpec `yaml:"policy"`
}

type StackSyncPolicySpec struct {
	// Prune overrides global prune policy for this stack when specified.
	Prune *bool `yaml:"prune"`
	// SelfHeal overrides global self-heal policy for this stack when specified.
	SelfHeal *bool `yaml:"selfHeal"`
}

type NetworkSpec struct {
	// Name is a Docker Swarm stack name.
	Name string `yaml:"name"`
	// Driver is a Docker network driver (for example: overlay, bridge).
	Driver string `yaml:"driver"`
	// Attachable allows standalone containers to attach to the network.
	Attachable bool `yaml:"attachable"`
	// Internal marks network as internal-only.
	Internal bool `yaml:"internal"`
	// Labels contains custom Docker network labels.
	Labels map[string]string `yaml:"labels"`
	// Options contains driver-specific network options.
	Options map[string]string `yaml:"options"`
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

type DeploymentsSpec struct {
	// PollInterval controls polling for Init Jobs and service convergence.
	PollInterval specw.Duration `yaml:"pollInterval"`
	// InitJobs contains Init Job execution settings.
	InitJobs InitJobsSpec `yaml:"initJobs"`
}

type InitJobsSpec struct {
	// MaxDuration is the maximum execution time for an Init Job.
	MaxDuration specw.Duration `yaml:"maxDuration"`
}

type ResourceRotationSpec struct {
	// Enabled toggles secret/config name rotation.
	Enabled bool `yaml:"enabled"`
	// HashLength is a length of generated hash suffix.
	HashLength int `yaml:"hashLength"`
	// IncludePath adds source path into hash input.
	IncludePath bool `yaml:"includePath"`
	// Cleanup controls removal of old managed rotated resources.
	Cleanup ResourceRotationCleanupSpec `yaml:"cleanup"`
}

// AgeSpec configures file-based age identities for encrypted Docker secrets.
type AgeSpec struct {
	// IdentityFile loads private identities from a mounted file.
	IdentityFile specw.File `yaml:"identityFile" json:"-"`
}

// ResourceRotationCleanupSpec controls cleanup of old rotated configs and secrets.
type ResourceRotationCleanupSpec struct {
	// Enabled toggles best-effort cleanup after stack deployment and pruning.
	Enabled bool `yaml:"enabled"`
	// Interval throttles periodic cleanup attempts between reconciliations.
	Interval specw.Duration `yaml:"interval"`
	// KeepLast preserves this many newest generations for each logical resource.
	KeepLast int `yaml:"keepLast"`
	// MinAge prevents cleanup of resources younger than this duration.
	MinAge specw.Duration `yaml:"minAge"`
}

func (c *Config) applyDefaults(configDir string) error {
	c.Spec.DataDir = filepath.Join(configDir, ".swarm-deploy")
	c.applyGitAndSyncDefaults()
	c.applyWebhookDefaults()
	c.applyWebAndHealthDefaults()
	c.Spec.Notifications.applyDefaults()
	c.applyAssistantDefaults()
	c.applyDeploymentsDefaults()
	c.applyResourceRotationDefaults()
	c.applyEventHistoryDefaults()

	if err := os.MkdirAll(c.Spec.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", c.Spec.DataDir, err)
	}

	return nil
}

func (c *Config) applyGitAndSyncDefaults() {
	if c.Spec.GitOps.Git.Pull.Timeout.Value <= 0 {
		c.Spec.GitOps.Git.Pull.Timeout.Value = DefaultGitPullTimeout
	}
	if c.Spec.GitOps.Git.Branch == "" {
		c.Spec.GitOps.Git.Branch = "main"
	}
	if c.Spec.GitOps.Sync.Mode == "" {
		c.Spec.GitOps.Sync.Mode = SyncModeHybrid
	}
	if c.Spec.GitOps.Git.Pull.Interval.Value <= 0 {
		c.Spec.GitOps.Git.Pull.Interval.Value = defaultGitPullInterval
	}
	if c.Spec.GitOps.Sync.Interval.Value <= 0 {
		c.Spec.GitOps.Sync.Interval.Value = defaultSyncInterval
	}
	if c.Spec.GitOps.Sync.Parallelism <= 0 {
		c.Spec.GitOps.Sync.Parallelism = defaultSyncParallelism
	}
	if c.Spec.GitOps.Sync.Webhook.Path == "" {
		c.Spec.GitOps.Sync.Webhook.Path = "/api/v1/webhooks/git"
	}
	if strings.TrimSpace(c.Spec.GitOps.Sync.Webhook.Address) == "" {
		c.Spec.GitOps.Sync.Webhook.Address = defaultWebhookAddress
	}
	if c.Spec.GitOps.Sync.Mode == SyncModeWebhook && !c.Spec.GitOps.Sync.Webhook.Enabled {
		c.Spec.GitOps.Sync.Webhook.Enabled = true
	}
}

func (c *Config) applyWebAndHealthDefaults() {
	if strings.TrimSpace(c.Spec.Web.Address) == "" {
		c.Spec.Web.Address = defaultWebAddress
	}
	c.Spec.Web.BasePath = normalizeWebBasePath(c.Spec.Web.BasePath)
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

func normalizeWebBasePath(basePath string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == defaultWebBasePath {
		return defaultWebBasePath
	}

	// Normalize one optional trailing slash, but retain repeated separators
	// so validation can reject ambiguous paths rather than silently fixing them.
	if strings.HasSuffix(basePath, "/") && !strings.HasSuffix(basePath, "//") {
		return strings.TrimSuffix(basePath, "/")
	}

	return basePath
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

	if openaiCfg.MaxTokens == 0 {
		openaiCfg.MaxTokens = defaultAssistantMaxTokens
	}

	inMemoryStorageCfg := &c.Spec.Assistant.Conversation.Storage.InMemory
	if inMemoryStorageCfg.TTL.Value <= 0 {
		c.Spec.Assistant.Conversation.Storage.InMemory.TTL.Value = defaultAssistantConversationInMemoryTTL
	}
}

func (c *Config) applyDeploymentsDefaults() {
	if c.Spec.Deployments.PollInterval.Value <= 0 {
		c.Spec.Deployments.PollInterval.Value = defaultDeploymentPollInterval
	}
	if c.Spec.Deployments.InitJobs.MaxDuration.Value <= 0 {
		c.Spec.Deployments.InitJobs.MaxDuration.Value = defaultInitJobMaxDuration
	}
}

func (c *Config) applyResourceRotationDefaults() {
	if c.Spec.GitOps.ResourceRotation.HashLength <= 0 {
		c.Spec.GitOps.ResourceRotation.HashLength = 8
	}
	if c.Spec.GitOps.ResourceRotation.Cleanup.Interval.Value == 0 {
		c.Spec.GitOps.ResourceRotation.Cleanup.Interval.Value = defaultRotationCleanupInterval
	}
	if c.Spec.GitOps.ResourceRotation.Cleanup.KeepLast == 0 {
		c.Spec.GitOps.ResourceRotation.Cleanup.KeepLast = defaultRotationKeepLast
	}
	if c.Spec.GitOps.ResourceRotation.Cleanup.MinAge.Value == 0 {
		c.Spec.GitOps.ResourceRotation.Cleanup.MinAge.Value = defaultRotationMinAge
	}
}

func (c *Config) applyEventHistoryDefaults() {
	if c.Spec.Event.History.Capacity <= 0 {
		c.Spec.Event.History.Capacity = defaultEventHistoryCap
	}
}

func (c *Config) loadInitialDesired(configDir string) error {
	baseDirs := []string{filepath.Join(c.Spec.DataDir, "repo"), configDir}
	stacks, err := c.loadStacks(baseDirs...)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		stacks = nil
	}

	networks, err := c.loadNetworks(baseDirs...)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		networks = nil
	}

	c.mu.Lock()
	c.stacks = cloneStacks(stacks)
	c.networks = cloneNetworks(networks)
	c.mu.Unlock()
	return nil
}

// Reload loads and validates all reloadable configuration, then atomically
// replaces the accepted state when its effective value changed.
func (c *Config) Reload(baseDirs ...string) (bool, error) {
	stacks, err := c.loadStacks(baseDirs...)
	if err != nil {
		return false, err
	}
	networks, err := c.loadNetworks(baseDirs...)
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if reflect.DeepEqual(c.stacks, stacks) && reflect.DeepEqual(c.networks, networks) {
		return false, nil
	}
	c.stacks = cloneStacks(stacks)
	c.networks = cloneNetworks(networks)
	return true, nil
}

func (c *Config) loadStacks(baseDirs ...string) ([]StackSpec, error) {
	stacksPath, err := c.resolveStacksPath(baseDirs...)
	if err != nil {
		return nil, err
	}
	stacks, err := loadStacksFromFile(stacksPath)
	if err != nil {
		return nil, err
	}
	if errs := validateStacksList(stacks); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return stacks, nil
}

func (c *Config) loadNetworks(baseDirs ...string) ([]NetworkSpec, error) {
	if strings.TrimSpace(c.Spec.GitOps.Networks.File) == "" {
		return nil, nil
	}
	networksPath, err := c.resolveNetworksPath(baseDirs...)
	if err != nil {
		return nil, err
	}
	networks, err := loadNetworksFromFile(networksPath)
	if err != nil {
		return nil, err
	}
	if errs := validateNetworksList(networks); len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return networks, nil
}

func (c *Config) resolveStacksPath(baseDirs ...string) (string, error) {
	if c.Spec.GitOps.Stacks.File == "" {
		return "", errors.New("gitops.stacks.file is required")
	}

	if filepath.IsAbs(c.Spec.GitOps.Stacks.File) {
		return c.Spec.GitOps.Stacks.File, nil
	}

	var candidates []string
	for _, baseDir := range baseDirs {
		if strings.TrimSpace(baseDir) == "" {
			continue
		}

		candidate := filepath.Join(baseDir, c.Spec.GitOps.Stacks.File)
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
		return "", errors.New("gitops.stacks.file is relative and no baseDirs provided")
	}

	return "", fmt.Errorf(
		"stacks file %s not found in any base dir: %s: %w",
		c.Spec.GitOps.Stacks.File,
		strings.Join(candidates, ", "),
		os.ErrNotExist,
	)
}

func (c *Config) resolveNetworksPath(baseDirs ...string) (string, error) {
	if c.Spec.GitOps.Networks.File == "" {
		return "", errors.New("gitops.networks.file is required")
	}

	if filepath.IsAbs(c.Spec.GitOps.Networks.File) {
		return c.Spec.GitOps.Networks.File, nil
	}

	var candidates []string
	for _, baseDir := range baseDirs {
		if strings.TrimSpace(baseDir) == "" {
			continue
		}

		candidate := filepath.Join(baseDir, c.Spec.GitOps.Networks.File)
		candidates = append(candidates, candidate)

		_, err := os.Stat(candidate)
		if err == nil {
			return candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat networks file %s: %w", candidate, err)
		}
	}

	if len(candidates) == 0 {
		return "", errors.New("gitops.networks.file is relative and no baseDirs provided")
	}

	return "", fmt.Errorf(
		"networks file %s not found in any base dir: %s: %w",
		c.Spec.GitOps.Networks.File,
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
		Stacks *[]StackSpec `yaml:"stacks"`
	}

	var document yaml.Node
	if err = yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode stacks file %s: %w", stacksPath, err)
	}
	if len(document.Content) == 0 {
		return nil, fmt.Errorf("stacks file %s does not contain stacks", stacksPath)
	}

	root := document.Content[0]
	if root.Kind == yaml.MappingNode {
		var container stacksContainer
		if err = root.Decode(&container); err != nil {
			return nil, fmt.Errorf("decode stacks file %s: %w", stacksPath, err)
		}
		if container.Stacks == nil {
			return nil, fmt.Errorf("stacks file %s does not contain stacks", stacksPath)
		}
		if len(*container.Stacks) == 0 {
			return nil, nil
		}
		return *container.Stacks, nil
	}
	if root.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("stacks file %s must contain a stacks mapping or list", stacksPath)
	}

	var list []StackSpec
	if err = root.Decode(&list); err != nil {
		return nil, fmt.Errorf("decode stacks list %s: %w", stacksPath, err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

func loadNetworksFromFile(networksPath string) ([]NetworkSpec, error) {
	data, err := os.ReadFile(networksPath)
	if err != nil {
		return nil, fmt.Errorf("read networks file %s: %w", networksPath, err)
	}

	var document yaml.Node
	if err = yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode networks file %s: %w", networksPath, err)
	}
	if len(document.Content) == 0 {
		return nil, fmt.Errorf("networks file %s does not contain networks", networksPath)
	}

	root := document.Content[0]
	networks, err := decodeNetworks(root, networksPath)
	if err != nil {
		return nil, err
	}
	if len(networks) == 0 {
		return nil, nil
	}

	for i := range networks {
		networks[i].Name = strings.TrimSpace(networks[i].Name)
		networks[i].Driver = strings.TrimSpace(networks[i].Driver)
		if networks[i].Driver == "" {
			networks[i].Driver = defaultManagedNetworkDriver
		}
	}
	return networks, nil
}

func decodeNetworks(root *yaml.Node, networksPath string) ([]NetworkSpec, error) {
	if root.Kind == yaml.MappingNode {
		var container struct {
			Networks *[]NetworkSpec `yaml:"networks"`
		}
		if err := root.Decode(&container); err != nil {
			return nil, fmt.Errorf("decode networks file %s: %w", networksPath, err)
		}
		if container.Networks == nil {
			return nil, fmt.Errorf("networks file %s does not contain networks", networksPath)
		}
		return *container.Networks, nil
	}
	if root.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("networks file %s must contain a networks mapping or list", networksPath)
	}

	var networks []NetworkSpec
	if err := root.Decode(&networks); err != nil {
		return nil, fmt.Errorf("decode networks list %s: %w", networksPath, err)
	}
	return networks, nil
}

func (c *Config) validate() error {
	var errs []error

	errs = append(errs, c.validateRequired()...)
	errs = append(errs, c.validateStacks()...)
	errs = append(errs, c.validateNetworks()...)
	errs = append(errs, c.validateSync()...)
	errs = append(errs, c.validateResourceRotation()...)
	errs = append(errs, c.validateGitAuth()...)
	errs = append(errs, c.validateWeb()...)
	errs = append(errs, c.validateSecurity()...)
	errs = append(errs, c.Spec.Notifications.validate()...)
	errs = append(errs, c.validateAssistant()...)
	errs = append(errs, c.validateTracing()...)

	return errors.Join(errs...)
}

func (c *Config) validateWeb() []error {
	basePath := c.Spec.Web.BasePath
	if !strings.HasPrefix(basePath, "/") {
		return []error{errors.New("web.basePath must start with /")}
	}
	if strings.ContainsAny(basePath, "?#\\") {
		return []error{errors.New("web.basePath must be a URL path without query, fragment, or backslash")}
	}
	if strings.Contains(basePath, "//") {
		return []error{errors.New("web.basePath must not contain empty path segments")}
	}
	if strings.Contains(basePath, "%") {
		return []error{errors.New("web.basePath must not contain percent-encoded segments")}
	}
	if basePath == "/" {
		return nil
	}

	for _, segment := range strings.Split(strings.TrimPrefix(basePath, "/"), "/") {
		if segment == "." || segment == ".." {
			return []error{errors.New("web.basePath must not contain . or .. path segments")}
		}
		// URL-unreserved ASCII characters cannot introduce ServeMux wildcards,
		// path escapes, or ambiguity between decoded and literal route prefixes.
		for _, char := range segment {
			if (char >= 'a' && char <= 'z') ||
				(char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') ||
				char == '-' || char == '_' || char == '.' || char == '~' {
				continue
			}
			return []error{fmt.Errorf(
				"web.basePath contains invalid character %q; only URL-unreserved characters are allowed",
				char,
			)}
		}
	}

	return nil
}

func (c *Config) validateResourceRotation() []error {
	cleanup := c.Spec.GitOps.ResourceRotation.Cleanup
	if !cleanup.Enabled {
		return nil
	}

	var errs []error
	if !c.Spec.GitOps.ResourceRotation.Enabled {
		errs = append(errs, errors.New(
			"gitops.resourceRotation.cleanup.enabled requires gitops.resourceRotation.enabled=true",
		))
	}
	if cleanup.Interval.Value < 0 {
		errs = append(errs, errors.New("gitops.resourceRotation.cleanup.interval must be > 0"))
	}
	if cleanup.KeepLast < 1 {
		errs = append(errs, errors.New("gitops.resourceRotation.cleanup.keepLast must be >= 1"))
	}
	if cleanup.MinAge.Value < 0 {
		errs = append(errs, errors.New("gitops.resourceRotation.cleanup.minAge must be > 0"))
	}

	return errs
}

func (c *Config) validateRequired() []error {
	var errs []error

	if c.Spec.GitOps.Git.Repository == "" {
		errs = append(errs, errors.New("gitops.git.repository is required"))
	}
	if c.Spec.GitOps.Stacks.File == "" {
		errs = append(errs, errors.New("gitops.stacks.file is required"))
	}

	return errs
}

func (c *Config) validateStacks() []error {
	stacks := c.Stacks()
	if len(stacks) == 0 {
		return nil
	}

	return validateStacksList(stacks)
}

func (c *Config) validateNetworks() []error {
	networks := c.Networks()
	if len(networks) == 0 {
		return nil
	}

	return validateNetworksList(networks)
}

func validateStacksList(stacks []StackSpec) []error {
	var errs []error

	seen := map[string]struct{}{}
	for i, stack := range stacks {
		if stack.Name == "" {
			errs = append(errs, fmt.Errorf("gitops.stacks.file[%d].name is required", i))
		}
		if stack.ComposeFile == "" {
			errs = append(errs, fmt.Errorf("gitops.stacks.file[%d].composeFile is required", i))
		}
		if _, exists := seen[stack.Name]; exists {
			errs = append(errs, fmt.Errorf("gitops.stacks.file has duplicated name %q", stack.Name))
		}
		seen[stack.Name] = struct{}{}
	}

	return errs
}

func validateNetworksList(networks []NetworkSpec) []error {
	var errs []error

	seen := map[string]struct{}{}
	for i, network := range networks {
		if network.Name == "" {
			errs = append(errs, fmt.Errorf("gitops.networks.file[%d].name is required", i))
		}
		if network.Driver == "" {
			errs = append(errs, fmt.Errorf("gitops.networks.file[%d].driver is required", i))
		}
		if _, exists := seen[network.Name]; exists {
			errs = append(errs, fmt.Errorf("gitops.networks.file has duplicated name %q", network.Name))
		}
		if labelValue, exists := network.Labels[managedNetworkLabelKey]; exists {
			if strings.TrimSpace(labelValue) != managedNetworkLabelValue {
				errs = append(
					errs,
					fmt.Errorf(
						"gitops.networks.file[%d].labels[%q] must be %q when specified",
						i,
						managedNetworkLabelKey,
						managedNetworkLabelValue,
					),
				)
			}
		}
		seen[network.Name] = struct{}{}
	}

	return errs
}

func (c *Config) validateSync() []error {
	var errs []error

	switch c.Spec.GitOps.Sync.Mode {
	case SyncModePull, SyncModeWebhook, SyncModeHybrid:
	default:
		errs = append(errs, fmt.Errorf(
			"gitops.sync.mode must be one of %q|%q|%q", SyncModePull, SyncModeWebhook, SyncModeHybrid,
		))
	}

	if c.Spec.GitOps.Sync.Webhook.Enabled && c.Spec.GitOps.Sync.Mode == SyncModePull {
		errs = append(errs, errors.New("gitops.sync.webhook.enabled=true conflicts with gitops.sync.mode=pull"))
	}
	errs = append(errs, c.validateWebhook()...)

	return errs
}

func (c *Config) validateGitAuth() []error {
	var errs []error

	authType := c.Spec.GitOps.Git.Auth.Type
	if !authType.IsSupported() {
		errs = append(errs, fmt.Errorf(
			"gitops.git.auth.type must be one of none|http|ssh, got %q", authType,
		))
	}

	if authType == GitAuthTypeHTTP {
		username := strings.TrimSpace(c.Spec.GitOps.Git.Auth.HTTP.Username)
		password := strings.TrimSpace(string(c.Spec.GitOps.Git.Auth.HTTP.Password.Content))
		token := strings.TrimSpace(string(c.Spec.GitOps.Git.Auth.HTTP.Token.Content))

		if token != "" && password != "" {
			errs = append(errs, errors.New(
				"gitops.git.auth.http.tokenPath and gitops.git.auth.http.passwordPath are mutually exclusive",
			))
		}
		if token == "" && (username == "" || password == "") {
			errs = append(
				errs,
				errors.New("gitops.git.auth.http requires username+passwordPath or tokenPath"),
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

	if c.Spec.Assistant.Model.OpenAI.MaxTokens <= 0 {
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

// Strategy resolves configured web authentication strategy.
func (a AuthenticationSpec) Strategy() string {
	if strings.TrimSpace(a.AuthProxy.LoginHeader) != "" {
		return AuthenticationStrategyAuthProxy
	}
	if strings.TrimSpace(a.Basic.HTPasswdFile.Path) != "" {
		return AuthenticationStrategyBasic
	}

	return AuthenticationStrategyNone
}
