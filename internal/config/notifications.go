package config

import (
	"fmt"
	"strings"

	"github.com/artarts36/specw"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	eventnotify "github.com/swarm-deploy/swarm-deploy/internal/modules/event/notify"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
	"go.yaml.in/yaml/v3"
)

const defaultNotificationTelegramRetries = 3

// NotificationSpec defines explicit event subscriptions and messenger destinations.
// Alert lifecycle routing is introduced separately.
type NotificationSpec struct {
	Messengers	NotificationMessengersSpec `yaml:"messengers"`
	Events	map[events.TypeName]NotificationEventRoutes `yaml:"events"`
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
	Custom	[]CustomChannel   `yaml:"custom"`
}

type NotificationMessengersSpec struct {
	Telegram NotificationTelegramSpec `yaml:"telegram"`
}

type NotificationTelegramSpec struct {
	BotToken specw.File                      `yaml:"botTokenPath"`
	Chats	map[string]NotificationChatSpec `yaml:"chats"`
	Retries	uint                            `yaml:"retries"`
	Proxy	NotificationTelegramProxySpec   `yaml:"proxy"`
}

type NotificationChatSpec struct {
	ID	string `yaml:"id"`
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
	Chats	NotificationChatNames `yaml:"chats"`
	Message string                `yaml:"message"`
}

func (c TelegramChannel) ChatNames() []string {
	if c.Chats == nil {
		return []string{"default"}
	}
	return []string(c.Chats)
}

type CustomChannel struct {
	Name	string                   `yaml:"name"`
	URL	specw.Env[specw.URL]     `yaml:"url"`
	Method string                   `yaml:"method"`
	Header map[string]string        `yaml:"header"`
}

func (c *NotificationSpec) applyDefaults() {
	if c.Messengers.Telegram.Retries == 0 {
		c.Messengers.Telegram.Retries = defaultNotificationTelegramRetries
	}
}

func (c *NotificationSpec) validate() []error {
	var errs []error
	errs = append(errs, c.validateTelegramMessenger()...)
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
