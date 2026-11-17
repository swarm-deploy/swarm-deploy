package tools

import (
	"slices"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type globalConfiguration struct {
	overview     configOverview
	imageTag     imageTagSettings
	events       []eventNotificationSettings
	alertRouting []alertRouteSettings
	allowedTools *[]string
}

type configOverview struct {
	GitOps        gitOpsSettings       `json:"gitops"`
	Deployments   deploymentSettings   `json:"deployments"`
	Event         eventSettings        `json:"event"`
	System        systemSettings       `json:"system"`
	Notifications notificationSettings `json:"notifications"`
	Web           webSettings          `json:"web"`
	HealthServer  healthServerSettings `json:"healthServer"`
	Assistant     assistantSettings    `json:"assistant"`
	Hostings      hostingSettings      `json:"hostings"`
}

type gitOpsSettings struct {
	SyncMode          string       `json:"syncMode"`
	ReconcileInterval string       `json:"reconcileInterval"`
	GitPollingEnabled bool         `json:"gitPollingEnabled"`
	GitPollInterval   string       `json:"gitPollInterval,omitempty"`
	WebhookEnabled    bool         `json:"webhookEnabled"`
	GlobalPolicy      gitOpsPolicy `json:"globalPolicy"`
}

type gitOpsPolicy struct {
	SelfHeal bool              `json:"selfHeal"`
	Prune    bool              `json:"prune"`
	ImageTag *imageTagSettings `json:"imageTag,omitempty"`
}

type imageTagSettings struct {
	Required bool `json:"required"`
	NoLatest bool `json:"noLatest"`
	OnlySHA  bool `json:"onlySHA"`
}

type deploymentSettings struct {
	PollInterval          string `json:"pollInterval"`
	InitJobDefaultTimeout string `json:"initJobDefaultTimeout"`
}

type eventSettings struct {
	HistoryCapacity int `json:"historyCapacity"`
}

type systemSettings struct {
	LogLevel string          `json:"logLevel"`
	Tracing  tracingSettings `json:"tracing"`
}

type tracingSettings struct {
	Enabled bool             `json:"enabled"`
	Sampler *samplerSettings `json:"sampler,omitempty"`
}

type samplerSettings struct {
	Strategy string   `json:"strategy"`
	Ratio    *float64 `json:"ratio,omitempty"`
}

type notificationSettings struct {
	TelegramConfigured           bool     `json:"telegramConfigured"`
	TelegramMaxAttemptsPerAction uint     `json:"telegramMaxAttemptsPerAction,omitempty"`
	EventTypes                   []string `json:"eventTypes"`
}

type eventNotificationSettings struct {
	Type     string   `json:"type"`
	Channels []string `json:"channels"`
}

type alertRouteSettings struct {
	Kind                 string   `json:"kind"`
	Source               string   `json:"source"`
	ConfiguredRecipients int      `json:"configuredRecipients"`
	RecipientPolicy      string   `json:"recipientPolicy,omitempty"`
	ResolvedActions      []string `json:"resolvedActions,omitempty"`
}

type webSettings struct {
	BasePath       string `json:"basePath"`
	Authentication string `json:"authentication"`
}

type healthServerSettings struct {
	HealthcheckPath string `json:"healthcheckPath,omitempty"`
	MetricsPath     string `json:"metricsPath,omitempty"`
}

type assistantSettings struct {
	Enabled                 bool               `json:"enabled"`
	ToolAccess              toolAccessSettings `json:"toolAccess"`
	MaxOutputTokens         int                `json:"maxOutputTokens"`
	ConversationInMemoryTTL string             `json:"conversationInMemoryTTL"`
}

type toolAccessSettings struct {
	Mode         string    `json:"mode"`
	AllowedTools *[]string `json:"allowedTools,omitempty"`
}

type hostingSettings struct {
	Integrations []hostingIntegration `json:"integrations"`
}

type hostingIntegration struct {
	Type     string `json:"type"`
	AuthMode string `json:"authMode"`
}

func projectGlobalConfiguration(
	spec config.Spec, allowed map[string]struct{}, builtins []routing.Tool,
) globalConfiguration {
	gitOps := projectGitOps(spec.GitOps)
	imageTag := spec.GitOps.Sync.Policy.Image.Tag
	notifications, configuredEvents := projectEventNotifications(spec.Notifications)
	assistant, allowedNames := projectAssistant(spec.Assistant, allowed, builtins)

	authMode := "anonymous"
	if spec.Hostings.GitHub.Token != "" {
		authMode = "authenticated"
	}

	return globalConfiguration{
		overview: configOverview{
			GitOps: gitOps,
			Deployments: deploymentSettings{
				PollInterval:          configDuration(spec.Deployments.PollInterval.Value),
				InitJobDefaultTimeout: configDuration(spec.Deployments.InitJobs.MaxDuration.Value),
			},
			Event:         eventSettings{HistoryCapacity: spec.Event.History.Capacity},
			System:        projectSystem(spec.System),
			Notifications: notifications,
			Web:           webSettings{BasePath: spec.Web.BasePath, Authentication: spec.Web.Security.Authentication.Strategy()},
			HealthServer: healthServerSettings{
				HealthcheckPath: httpRoutePath(spec.HealthServer.Healthz.Path),
				MetricsPath:     httpRoutePath(spec.HealthServer.Metrics.Path),
			},
			Assistant: assistant,
			Hostings:  hostingSettings{Integrations: []hostingIntegration{{Type: "github", AuthMode: authMode}}},
		},
		imageTag:     imageTagSettings{Required: imageTag.Required, NoLatest: imageTag.NoLatest, OnlySHA: imageTag.OnlySHA},
		events:       configuredEvents,
		alertRouting: projectAlertRouting(spec.Notifications, notifications.TelegramConfigured),
		allowedTools: allowedNames,
	}
}

func (c *globalConfiguration) section(section string) any {
	switch section {
	case "gitops":
		settings := c.overview.GitOps
		settings.GlobalPolicy.ImageTag = &c.imageTag
		return settings
	case "deployments":
		return c.overview.Deployments
	case "event":
		return c.overview.Event
	case "system":
		return c.overview.System
	case "notifications":
		return struct {
			notificationSettings
			Events       []eventNotificationSettings `json:"events"`
			AlertRouting []alertRouteSettings        `json:"alertRouting"`
		}{c.overview.Notifications, c.events, c.alertRouting}
	case "web":
		return c.overview.Web
	case "healthServer":
		return c.overview.HealthServer
	case "assistant":
		settings := c.overview.Assistant
		settings.ToolAccess.AllowedTools = c.allowedTools
		return settings
	case "hostings":
		return c.overview.Hostings
	default:
		return c.overview
	}
}

func projectGitOps(spec config.GitOpsSpec) gitOpsSettings {
	settings := gitOpsSettings{
		SyncMode:          spec.Sync.Mode,
		ReconcileInterval: configDuration(spec.Sync.Interval.Value),
		GitPollingEnabled: spec.Sync.GitPollingEnabled(),
		WebhookEnabled:    spec.Sync.Webhook.Enabled,
		GlobalPolicy:      gitOpsPolicy{SelfHeal: spec.Sync.Policy.SelfHeal, Prune: spec.Sync.Policy.Prune},
	}
	if settings.GitPollingEnabled {
		settings.GitPollInterval = configDuration(spec.Git.Pull.Interval.Value)
	}
	return settings
}

func projectSystem(spec config.SystemSpec) systemSettings {
	settings := systemSettings{LogLevel: spec.Log.Level.Level().String()}
	if spec.Tracing == nil {
		return settings
	}
	sampler := spec.Tracing.Sampler
	settings.Tracing = tracingSettings{Enabled: true, Sampler: &samplerSettings{Strategy: sampler.Strategy.Value}}
	if sampler.UsesRatio() {
		settings.Tracing.Sampler.Ratio = &sampler.Ratio.Value
	}
	return settings
}

func projectEventNotifications(spec config.NotificationSpec) (notificationSettings, []eventNotificationSettings) {
	settings := notificationSettings{
		TelegramConfigured: len(spec.Messengers.Telegram.BotToken.Content) > 0,
		EventTypes:         make([]string, 0, len(spec.Events)),
	}
	if settings.TelegramConfigured {
		settings.TelegramMaxAttemptsPerAction = spec.Messengers.Telegram.Retries
	}

	configured := make([]eventNotificationSettings, 0, len(spec.Events))
	for eventType, routes := range spec.Events {
		if !eventType.Valid() || eventType == events.TypeNameSendNotificationFailed {
			continue
		}
		var channels []string
		if settings.TelegramConfigured && len(routes.Telegram) > 0 {
			channels = append(channels, "telegram")
		}
		if len(routes.Custom) > 0 {
			channels = append(channels, "custom")
		}
		if len(channels) > 0 {
			configured = append(configured, eventNotificationSettings{Type: string(eventType), Channels: channels})
		}
	}
	slices.SortFunc(configured, func(a, b eventNotificationSettings) int { return strings.Compare(a.Type, b.Type) })
	for _, event := range configured {
		settings.EventTypes = append(settings.EventTypes, event.Type)
	}
	return settings, configured
}

func projectAlertRouting(spec config.NotificationSpec, telegramConfigured bool) []alertRouteSettings {
	kinds := [...]alertmodel.AlertKind{
		alertmodel.AlertKindSyncFailed, alertmodel.AlertKindNodeDisconnected, alertmodel.AlertKindGitRepositoryUnavailable,
		alertmodel.AlertKindServiceDrift, alertmodel.AlertKindMessengerUnavailable,
		alertmodel.AlertKindMessengerMisconfigured,
	}
	routingSettings := make([]alertRouteSettings, 0, len(kinds))
	for _, kind := range kinds {
		routes, source := spec.AlertTelegramRoutesWithSource(string(kind))
		settings := alertRouteSettings{Kind: string(kind), Source: source}
		if telegramConfigured {
			settings.ConfiguredRecipients = len(spec.AlertRecipients(string(kind)))
			switch kind {
			case alertmodel.AlertKindMessengerUnavailable:
				settings.ConfiguredRecipients = 0
				settings.RecipientPolicy = "disabledForMessengerUnavailable"
			case alertmodel.AlertKindMessengerMisconfigured:
				settings.RecipientPolicy = "excludeFailingDestination"
			case alertmodel.AlertKindSyncFailed, alertmodel.AlertKindNodeDisconnected,
				alertmodel.AlertKindGitRepositoryUnavailable, alertmodel.AlertKindServiceDrift:
			}
		}
		if settings.ConfiguredRecipients > 0 {
			for _, route := range routes {
				action := route.AlertResolvedAction()
				if (action == "reply" || action == "edit") && !slices.Contains(settings.ResolvedActions, action) {
					settings.ResolvedActions = append(settings.ResolvedActions, action)
				}
			}
			slices.Sort(settings.ResolvedActions)
		}
		routingSettings = append(routingSettings, settings)
	}
	return routingSettings
}

func projectAssistant(
	spec config.AssistantSpec, allowed map[string]struct{}, builtins []routing.Tool,
) (assistantSettings, *[]string) {
	settings := assistantSettings{
		Enabled:                 spec.Enabled,
		ToolAccess:              toolAccessSettings{Mode: "unrestricted"},
		MaxOutputTokens:         spec.Model.OpenAI.MaxTokens,
		ConversationInMemoryTTL: configDuration(spec.Conversation.Storage.InMemory.TTL.Value),
	}
	if len(allowed) == 0 {
		return settings, nil
	}
	settings.ToolAccess.Mode = "allowlist"
	names := make([]string, 0, len(allowed))
	if _, ok := allowed[configGetToolName]; ok {
		names = append(names, configGetToolName)
	}
	for _, tool := range builtins {
		name := string(tool.Definition().Name)
		if _, ok := allowed[name]; ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return settings, &names
}

// httpRoutePath discards optional method and host qualifiers from a ServeMux pattern.
func httpRoutePath(pattern string) string {
	pathStart := strings.IndexByte(pattern, '/')
	if pathStart < 0 || strings.ContainsAny(pattern[pathStart:], "?#\\ \t\r\n") ||
		strings.HasPrefix(pattern[pathStart:], "//") || strings.HasSuffix(pattern[:pathStart], ":") {
		return ""
	}
	return pattern[pathStart:]
}

func configDuration(value time.Duration) string {
	formatted := value.String()
	if value != 0 && value%time.Minute == 0 {
		formatted = strings.TrimSuffix(formatted, "0s")
		if value%time.Hour == 0 {
			formatted = strings.TrimSuffix(formatted, "0m")
		}
	}
	return formatted
}
