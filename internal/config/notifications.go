package config

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/artarts36/specw"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotify "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
	"go.yaml.in/yaml/v3"
)

const (
	defaultNotificationTelegramRetries = 3

	notificationAlertNameDefault                  = "default"
	notificationAlertNameSyncFailed               = "SyncFailed"
	notificationAlertNameNodeDisconnected         = "NodeDisconnected"
	notificationAlertNameGitRepositoryUnavailable = "GitRepositoryUnavailable"
	notificationAlertNameMessengerUnavailable     = "MessengerUnavailable"
	notificationAlertNameMessengerMisconfigured   = "MessengerMisconfigured"
)

// NotificationSpec defines event subscriptions, alert routes and messenger destinations.
type NotificationSpec struct {
	Messengers NotificationMessengersSpec                  `yaml:"messengers"`
	Events     map[events.TypeName]NotificationEventRoutes `yaml:"events"`
	Alerts     map[string]NotificationAlertRoutes          `yaml:"alerts"`
}

// NotificationAlertRoutes configures lifecycle messages for one alert kind or default.
type NotificationAlertRoutes struct {
	// Telegram contains one or more independently delivered Telegram routes.
	Telegram []TelegramAlertChannel `yaml:"telegram"`
}

// TelegramAlertChannel defines one lifecycle route to named chats.
type TelegramAlertChannel struct {
	// Chats selects named Telegram destinations, defaulting to "default".
	Chats NotificationChatNames `yaml:"chats"`
	// Opened describes the initial message.
	Opened AlertMessageSpec `yaml:"opened"`
	// Resolved describes the resolution message and operation.
	Resolved AlertMessageSpec `yaml:"resolved"`
}

// AlertMessageSpec configures one lifecycle phase.
type AlertMessageSpec struct {
	// Action is "reply" or "edit" for resolved; omitted means reply.
	Action string `yaml:"action"`
	// Message is a template rendered against the public alert payload.
	Message string `yaml:"message"`
}

const (
	defaultAlertOpenedTemplate           = "🚨 {{ .Title }}\n{{ .Message }}"
	defaultSyncFailedAlertOpenedTemplate = `<b>🚨 {{ .Title }}</b>

<b>Stack:</b> <code>{{ .ResourceID }}</code>

<b>Error:</b>
<pre>{{ .Message }}</pre>`
	defaultAlertResolvedTemplate = "✅ Resolved: {{ .Title }}\n{{ .Resolution }}"
	defaultGitOpenedTemplate     = "🚨 {{ .Title }}\nRepository: <code>{{ .ResourceID }}</code>\nError: {{ .Message }}" +
		"\nCategory: {{ .ErrorCode }}\nFailed attempts: {{ .Occurrences }}"
	defaultGitResolvedTemplate = "✅ Git repository recovered\nRepository: <code>{{ .ResourceID }}</code>" +
		"\nFailed attempts: {{ .Occurrences }}\nIncident duration: {{ .Duration }}"
)

// AlertTelegramRoutes returns the replacing kind route, configured default or built-in route.
func (c NotificationSpec) AlertTelegramRoutes(kind string) []TelegramAlertChannel {
	if route, ok := c.Alerts[notificationAlertConfigKey(alertmodel.AlertKind(kind))]; ok {
		return route.Telegram
	}
	if route, ok := c.Alerts[notificationAlertNameDefault]; ok {
		return route.Telegram
	}
	return []TelegramAlertChannel{{
		Resolved: AlertMessageSpec{Action: "reply"},
	}}
}

// AlertRecipients captures the configured physical Telegram destinations for an opening alert.
func (c NotificationSpec) AlertRecipients(kind string) []events.AlertRecipient {
	identity := c.TelegramBotIdentity()
	if identity == "" {
		return nil
	}
	var recipients []events.AlertRecipient
	seen := make(map[events.AlertRecipient]bool)
	for _, route := range c.AlertTelegramRoutes(kind) {
		for _, name := range (TelegramChannel{Chats: route.Chats}).ChatNames() {
			chat := c.Messengers.Telegram.Chats[name]
			recipient := events.AlertRecipient{BotIdentity: identity, ChatID: chat.ID, ThreadID: chat.ThreadID}
			if !seen[recipient] {
				recipients = append(recipients, recipient)
				seen[recipient] = true
			}
		}
	}
	return recipients
}

// AlertRecipientsFor returns alert routes after excluding the failing messenger destination.
func (c NotificationSpec) AlertRecipientsFor(kind, resourceID string) []events.AlertRecipient {
	recipients := c.AlertRecipients(kind)
	switch alertmodel.AlertKind(kind) {
	case alertmodel.AlertKindMessengerUnavailable:
		return nil
	case alertmodel.AlertKindMessengerMisconfigured:
		channelName, ok := strings.CutPrefix(resourceID, "telegram:")
		if !ok {
			return nil
		}
		chat, ok := c.Messengers.Telegram.Chats[channelName]
		if !ok {
			return nil
		}
		failed := events.AlertRecipient{
			BotIdentity: c.TelegramBotIdentity(), ChatID: chat.ID, ThreadID: chat.ThreadID,
		}
		filtered := recipients[:0]
		for _, recipient := range recipients {
			if recipient != failed {
				filtered = append(filtered, recipient)
			}
		}
		return filtered
	case alertmodel.AlertKindSyncFailed, alertmodel.AlertKindNodeDisconnected,
		alertmodel.AlertKindGitRepositoryUnavailable:
		return recipients
	default:
		return recipients
	}
}

// TelegramBotIdentity returns a stable non-secret identity for the configured bot.
func (c NotificationSpec) TelegramBotIdentity() string {
	token := string(c.Messengers.Telegram.BotToken.Content)
	if token == "" {
		return ""
	}
	botID, _, _ := strings.Cut(token, ":")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(botID)))
}

// AlertOpenedTemplate returns the effective initial message template.
func (c TelegramAlertChannel) AlertOpenedTemplate(kind string) string {
	if strings.TrimSpace(c.Opened.Message) != "" {
		return c.Opened.Message
	}
	if kind == string(alertmodel.AlertKindSyncFailed) {
		return defaultSyncFailedAlertOpenedTemplate
	}
	if kind == string(alertmodel.AlertKindGitRepositoryUnavailable) {
		return defaultGitOpenedTemplate
	}
	return defaultAlertOpenedTemplate
}

// AlertResolvedTemplate returns the effective resolution message template.
func (c TelegramAlertChannel) AlertResolvedTemplate(kind ...string) string {
	if strings.TrimSpace(c.Resolved.Message) != "" {
		return c.Resolved.Message
	}
	if len(kind) > 0 && kind[0] == string(alertmodel.AlertKindGitRepositoryUnavailable) {
		return defaultGitResolvedTemplate
	}
	return defaultAlertResolvedTemplate
}

// AlertResolvedAction returns the configured operation, defaulting to reply.
func (c TelegramAlertChannel) AlertResolvedAction() string {
	if c.Resolved.Action == "" {
		return "reply"
	}
	return c.Resolved.Action
}

// UnmarshalYAML rejects the removed "on" key instead of silently disabling old rules.
func (c *NotificationSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("notifications must be a mapping")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "on" {
			return fmt.Errorf("notifications.on is no longer supported; use notifications.events")
		}
	}
	type raw NotificationSpec
	return node.Decode((*raw)(c))
}

type NotificationEventRoutes struct {
	Telegram []TelegramChannel `yaml:"telegram"`
	Custom   []CustomChannel   `yaml:"custom"`
}

type NotificationMessengersSpec struct {
	Telegram NotificationTelegramSpec `yaml:"telegram"`
}

type NotificationTelegramSpec struct {
	BotToken specw.File                      `yaml:"botTokenPath"`
	Chats    map[string]NotificationChatSpec `yaml:"chats"`
	Retries  uint                            `yaml:"retries"`
	Proxy    NotificationTelegramProxySpec   `yaml:"proxy"`
}

type NotificationChatSpec struct {
	ID       string `yaml:"id"`
	ThreadID int64  `yaml:"threadId"`
}

type NotificationTelegramProxySpec struct {
	SOCKS5 NotificationTelegramSOCKS5Spec `yaml:"socks5"`
}

type NotificationTelegramSOCKS5Spec struct {
	Address specw.Env[string] `yaml:"address"`
}

// NotificationChatNames accepts either a single chat name or a list of names.
// Unspecified values select "default"; explicitly empty lists are invalid.
type NotificationChatNames []string

func (names *NotificationChatNames) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return fmt.Errorf("chats must be a string or an array of strings")
		}
		*names = NotificationChatNames{node.Value}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := node.Decode(&values); err != nil {
			return fmt.Errorf("decode chats: %w", err)
		}
		*names = NotificationChatNames(values)
		return nil
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
		return fmt.Errorf("chats must be a string or an array of strings")
	default:
		return fmt.Errorf("chats must be a string or an array of strings")
	}
}

type TelegramChannel struct {
	Chats   NotificationChatNames `yaml:"chats"`
	Message string                `yaml:"message"`
}

func (c TelegramChannel) ChatNames() []string {
	if c.Chats == nil {
		return []string{"default"}
	}
	return []string(c.Chats)
}

type CustomChannel struct {
	Name   string               `yaml:"name"`
	URL    specw.Env[specw.URL] `yaml:"url"`
	Method string               `yaml:"method"`
	Header map[string]string    `yaml:"header"`
}

func (c *NotificationSpec) applyDefaults() {
	if c.Messengers.Telegram.Retries == 0 {
		c.Messengers.Telegram.Retries = defaultNotificationTelegramRetries
	}
}

func (c *NotificationSpec) validate() []error {
	var errs []error
	errs = append(errs, c.validateTelegramMessenger()...)
	err := c.validateAlertRoutes()
	errs = append(errs, err...)
	for eventTypeName, routes := range c.Events {
		if !eventTypeName.Valid() {
			errs = append(errs, fmt.Errorf(
				"notifications.events[%q] has unknown or non-public event type", eventTypeName,
			))
			continue
		}
		errs = append(errs, c.validateTelegramEventRoutes(eventTypeName, routes.Telegram)...)
		errs = append(errs, validateCustomEventRoutes(eventTypeName, routes.Custom)...)
	}
	return errs
}

func (c *NotificationSpec) validateTelegramMessenger() []error {
	var errs []error
	telegram := c.Messengers.Telegram
	for name, chat := range telegram.Chats {
		path := fmt.Sprintf("notifications.messengers.telegram.chats[%q]", name)
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Errorf("telegram chat name cannot be empty"))
		}
		if strings.TrimSpace(chat.ID) == "" {
			errs = append(errs, fmt.Errorf("%s.id is required", path))
		}
		if chat.ThreadID < 0 {
			errs = append(errs, fmt.Errorf("%s.threadId must be >= 0", path))
		}
	}
	if len(telegram.Chats) > 0 && len(telegram.BotToken.Content) == 0 {
		errs = append(errs, fmt.Errorf(
			"notifications.messengers.telegram.botTokenPath contains empty token",
		))
	}
	if len(telegram.BotToken.Content) > 0 {
		if _, ok := telegram.Chats["default"]; !ok {
			errs = append(errs, fmt.Errorf(
				"notifications.messengers.telegram.chats[\"default\"] is required for alert fallback",
			))
		}
	}
	return errs
}

func notificationAlertConfigKey(kind alertmodel.AlertKind) string {
	switch kind {
	case alertmodel.AlertKindSyncFailed:
		return notificationAlertNameSyncFailed
	case alertmodel.AlertKindNodeDisconnected:
		return notificationAlertNameNodeDisconnected
	case alertmodel.AlertKindGitRepositoryUnavailable:
		return notificationAlertNameGitRepositoryUnavailable
	case alertmodel.AlertKindMessengerUnavailable:
		return notificationAlertNameMessengerUnavailable
	case alertmodel.AlertKindMessengerMisconfigured:
		return notificationAlertNameMessengerMisconfigured
	default:
		return string(kind)
	}
}

func notificationAlertKind(configKey string) alertmodel.AlertKind {
	switch configKey {
	case notificationAlertNameSyncFailed:
		return alertmodel.AlertKindSyncFailed
	case notificationAlertNameNodeDisconnected:
		return alertmodel.AlertKindNodeDisconnected
	case notificationAlertNameGitRepositoryUnavailable:
		return alertmodel.AlertKindGitRepositoryUnavailable
	case notificationAlertNameMessengerUnavailable:
		return alertmodel.AlertKindMessengerUnavailable
	case notificationAlertNameMessengerMisconfigured:
		return alertmodel.AlertKindMessengerMisconfigured
	default:
		return alertmodel.AlertKind(configKey)
	}
}

func validNotificationAlertConfigKey(kind string) bool {
	switch kind {
	case notificationAlertNameDefault, notificationAlertNameSyncFailed,
		notificationAlertNameNodeDisconnected, notificationAlertNameGitRepositoryUnavailable,
		notificationAlertNameMessengerUnavailable, notificationAlertNameMessengerMisconfigured:
		return true
	default:
		return false
	}
}

func (c *NotificationSpec) validateAlertRoutes() []error {
	var errs []error
	for kind, routes := range c.Alerts {
		if !validNotificationAlertConfigKey(kind) {
			errs = append(errs, fmt.Errorf("notifications.alerts[%q]: unknown alert kind", kind))
			continue
		}
		path := fmt.Sprintf("notifications.alerts[%q].telegram", kind)
		if len(routes.Telegram) == 0 {
			errs = append(errs, fmt.Errorf("%s must contain at least one route", path))
		}
		for i, route := range routes.Telegram {
			routePath := fmt.Sprintf("%s[%d]", path, i)
			errs = append(errs, c.validateAlertChannel(routePath, kind, route)...)
		}
		errs = append(errs, c.validateAlertDestinations(path, routes.Telegram)...)
	}
	return errs
}

func (c *NotificationSpec) validateAlertDestinations(path string, routes []TelegramAlertChannel) []error {
	var errs []error
	seen := make(map[events.AlertRecipient]string)
	identity := c.TelegramBotIdentity()
	for i, route := range routes {
		routePath := fmt.Sprintf("%s[%d]", path, i)
		for _, name := range (TelegramChannel{Chats: route.Chats}).ChatNames() {
			chat, ok := c.Messengers.Telegram.Chats[name]
			if !ok {
				continue
			}
			recipient := events.AlertRecipient{
				BotIdentity: identity,
				ChatID:      chat.ID,
				ThreadID:    chat.ThreadID,
			}
			if firstPath, duplicate := seen[recipient]; duplicate {
				errs = append(errs, fmt.Errorf(
					"%s.chats resolves to a duplicate Telegram destination already selected by %s.chats",
					routePath, firstPath,
				))
				continue
			}
			seen[recipient] = routePath
		}
	}
	return errs
}

func (c *NotificationSpec) validateAlertChannel(path, kind string, route TelegramAlertChannel) []error {
	var errs []error
	if len(c.Messengers.Telegram.BotToken.Content) == 0 {
		errs = append(errs, fmt.Errorf("%s: notifications.messengers.telegram.botTokenPath is required", path))
	}
	errs = append(errs, c.validateChatNames(path, route.Chats)...)
	if route.Opened.Action != "" {
		errs = append(errs, fmt.Errorf("%s.opened.action is not supported", path))
	}
	if action := route.AlertResolvedAction(); action != "reply" && action != "edit" {
		errs = append(errs, fmt.Errorf("%s.resolved.action must be reply or edit", path))
	}
	payload := events.AlertSnapshot{
		Title: "Alert", Message: "details", ResourceID: "resource", DeploymentID: "deployment-id",
		OpenedAt: time.Now().UTC(),
	}
	if err := notificationtemplate.Validate(
		route.AlertOpenedTemplate(string(notificationAlertKind(kind))),
		payload,
	); err != nil {
		errs = append(errs, fmt.Errorf("%s.opened.message: invalid template: %w", path, err))
	}
	resolvedAt := time.Now().UTC()
	payload.ResolvedAt = &resolvedAt
	payload.Resolution = "Recovered"
	payload.ResolutionReason = "recovered"
	if err := notificationtemplate.Validate(route.AlertResolvedTemplate(), payload); err != nil {
		errs = append(errs, fmt.Errorf("%s.resolved.message: invalid template: %w", path, err))
	}
	return errs
}

func (c *NotificationSpec) validateTelegramEventRoutes(
	eventTypeName events.TypeName,
	rules []TelegramChannel,
) []error {
	var errs []error
	for i, rule := range rules {
		path := fmt.Sprintf("notifications.events[%q].telegram[%d]", eventTypeName, i)
		if len(c.Messengers.Telegram.BotToken.Content) == 0 {
			errs = append(errs, fmt.Errorf(
				"%s: notifications.messengers.telegram.botTokenPath is required", path,
			))
		}
		errs = append(errs, c.validateChatNames(path, rule.Chats)...)
		errs = append(errs, validateTelegramEventTemplate(eventTypeName, path, rule.Message)...)
	}
	return errs
}

func (c *NotificationSpec) validateChatNames(path string, names NotificationChatNames) []error {
	if names != nil && len(names) == 0 {
		return []error{fmt.Errorf("%s.chats must not be empty", path)}
	}
	if names == nil {
		names = NotificationChatNames{"default"}
	}
	var errs []error
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		switch {
		case strings.TrimSpace(name) == "":
			errs = append(errs, fmt.Errorf("%s.chats contains an empty name", path))
		case seen[name]:
			errs = append(errs, fmt.Errorf("%s.chats contains duplicate %q", path, name))
		default:
			if _, ok := c.Messengers.Telegram.Chats[name]; !ok {
				errs = append(errs, fmt.Errorf("%s.chats references unknown chat %q", path, name))
			}
		}
		seen[name] = true
	}
	return errs
}

func validateTelegramEventTemplate(eventTypeName events.TypeName, path, message string) []error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	payloads, ok := eventnotify.ReferencePayloads(eventTypeName)
	if !ok {
		return []error{fmt.Errorf("%s.message: notification payload is not defined", path)}
	}
	for _, payload := range payloads {
		if err := notificationtemplate.Validate(message, payload); err != nil {
			return []error{fmt.Errorf("%s.message: invalid template: %w", path, err)}
		}
	}
	return nil
}

func validateCustomEventRoutes(eventTypeName events.TypeName, channels []CustomChannel) []error {
	var errs []error
	for i, channel := range channels {
		if channel.URL.Value.String() == "" {
			errs = append(errs, fmt.Errorf(
				"notifications.events[%q].custom[%d].url or urlEnv is required",
				eventTypeName, i,
			))
		}
	}
	return errs
}
