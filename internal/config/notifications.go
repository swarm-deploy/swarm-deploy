package config

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/artarts36/specw"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

const (
	defaultNotificationTelegramRetries = 3
	defaultAlertCorrelationTTL         = 24 * time.Hour
)

type NotificationSpec struct {
	// Messengers contains global messenger settings used by notification channels.
	Messengers NotificationMessengersSpec `yaml:"messengers"`
	// Events maps event types to notification channels.
	Events map[events.TypeName]NotificationChannels `yaml:"events"`
	// Alerts describes how alert lifecycle changes are delivered.
	Alerts NotificationAlertsSpec `yaml:"alerts"`
	// On is a deprecated alias of Events kept for backward compatibility.
	On map[events.TypeName]NotificationChannels `yaml:"on"`
}

// NotificationChannels lists channels notified about one event type.
type NotificationChannels struct {
	// Telegram is a list of Telegram notification channels.
	Telegram []TelegramChannel `yaml:"telegram"`
	// Custom is a list of custom webhook notification channels.
	Custom []CustomChannel `yaml:"custom"`
}

// AlertNotificationMode selects how alert closing is delivered.
type AlertNotificationMode string

const (
	// AlertNotificationModeSend sends a separate message when an alert is opened and when it is resolved.
	AlertNotificationModeSend AlertNotificationMode = "send"
	// AlertNotificationModeEdit edits the "opened" message when an alert is resolved.
	AlertNotificationModeEdit AlertNotificationMode = "edit"
)

// NotificationAlertsSpec configures alert notifications.
type NotificationAlertsSpec struct {
	// Mode is send (default) or edit.
	Mode AlertNotificationMode `yaml:"mode"`
	// CorrelationTTL is how long a sent message may be edited; defaults to 24h.
	CorrelationTTL time.Duration `yaml:"correlationTtl"`
	// Telegram is a list of Telegram channels receiving alert notifications.
	Telegram []TelegramChannel `yaml:"telegram"`
}

// EventChannels returns notifications.events merged with the deprecated notifications.on.
// Entries of notifications.events take precedence over notifications.on for the same event type.
func (c *NotificationSpec) EventChannels() map[events.TypeName]NotificationChannels {
	merged := make(map[events.TypeName]NotificationChannels, len(c.Events)+len(c.On))
	for eventType, channels := range c.On {
		merged[eventType] = channels
	}
	for eventType, channels := range c.Events {
		merged[eventType] = channels
	}

	return merged
}

type NotificationMessengersSpec struct {
	// Telegram contains global Telegram settings used by Telegram channels.
	Telegram NotificationTelegramSpec `yaml:"telegram"`
}

type NotificationTelegramSpec struct {
	// Retries is a number of attempts to deliver Telegram notification.
	Retries uint `yaml:"retries"`
	// Proxy contains global proxy settings for Telegram notifications.
	Proxy NotificationTelegramProxySpec `yaml:"proxy"`
}

type NotificationTelegramProxySpec struct {
	// SOCKS5 contains SOCKS5 proxy settings.
	SOCKS5 NotificationTelegramSOCKS5Spec `yaml:"socks5"`
}

type NotificationTelegramSOCKS5Spec struct {
	// Address is a SOCKS5 endpoint in host:port format.
	Address specw.Env[string] `yaml:"address"`
}

type TelegramChannel struct {
	// Name is a logical channel name used in logs/diagnostics.
	Name string `yaml:"name"`
	// BotToken is a path to file containing Telegram bot token.
	BotToken specw.File `yaml:"botTokenPath,omitempty"`
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
	URL specw.Env[specw.URL] `yaml:"url"`
	// Method is an HTTP method for webhook delivery.
	Method string `yaml:"method"`
	// Header contains additional HTTP headers for webhook delivery.
	Header map[string]string `yaml:"header"`
}

func (c *NotificationSpec) applyDefaults() {
	if c.Messengers.Telegram.Retries <= 0 {
		c.Messengers.Telegram.Retries = defaultNotificationTelegramRetries
	}
	if c.Alerts.Mode == "" {
		c.Alerts.Mode = AlertNotificationModeSend
	}
	if c.Alerts.CorrelationTTL == 0 {
		c.Alerts.CorrelationTTL = defaultAlertCorrelationTTL
	}
	if len(c.On) > 0 {
		slog.Warn("[config] notifications.on is deprecated, use notifications.events")
	}
}

func (c *NotificationSpec) validate() []error {
	var errs []error

	errs = append(errs, validateEventChannels("notifications.on", c.On)...)
	errs = append(errs, validateEventChannels("notifications.events", c.Events)...)

	switch c.Alerts.Mode {
	case AlertNotificationModeSend, AlertNotificationModeEdit:
	default:
		errs = append(errs, fmt.Errorf("notifications.alerts.mode must be %q or %q",
			AlertNotificationModeSend, AlertNotificationModeEdit))
	}
	if c.Alerts.CorrelationTTL < 0 {
		errs = append(errs, errors.New("notifications.alerts.correlationTtl must be > 0"))
	}
	for i, tg := range c.Alerts.Telegram {
		errs = append(errs, validateTelegramChannel(fmt.Sprintf("notifications.alerts.telegram[%d]", i), tg)...)
	}

	return errs
}

func validateEventChannels(path string, byEvent map[events.TypeName]NotificationChannels) []error {
	var errs []error

	for eventTypeName, channels := range byEvent {
		if !eventTypeName.Valid() {
			errs = append(errs, fmt.Errorf("%s[%q] has unknown event type", path, eventTypeName))
			continue
		}

		for i, tg := range channels.Telegram {
			errs = append(errs, validateTelegramChannel(fmt.Sprintf("%s[%q].telegram[%d]", path, eventTypeName, i), tg)...)
		}

		for i, ch := range channels.Custom {
			if ch.URL.Value.String() == "" {
				errs = append(errs, fmt.Errorf("%s[%q].custom[%d].url or urlEnv is required", path, eventTypeName, i))
			}
		}
	}

	return errs
}

func validateTelegramChannel(path string, tg TelegramChannel) []error {
	var errs []error

	if tg.ChatID == "" {
		errs = append(errs, fmt.Errorf("%s.chatId is required", path))
	}
	if len(tg.BotToken.Content) == 0 {
		errs = append(errs, fmt.Errorf("%s.botTokenPath contains empty token", path))
	}
	if tg.ChatThreadID < 0 {
		errs = append(errs, fmt.Errorf("%s.chatThreadId must be >= 0", path))
	}

	return errs
}
