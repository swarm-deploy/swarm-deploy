package config

import (
	"fmt"

	"github.com/artarts36/specw"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

const defaultNotificationTelegramRetries = 3

type NotificationSpec struct {
	// Messengers contains global messenger settings used by notification channels.
	Messengers NotificationMessengersSpec `yaml:"messengers"`
	// On maps event types to notification channels.
	On map[events.Type]struct {
		// Telegram is a list of Telegram notification channels.
		Telegram []TelegramChannel `yaml:"telegram"`
		// Custom is a list of custom webhook notification channels.
		Custom []CustomChannel `yaml:"custom"`
	} `yaml:"on"`
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
}

func (c *NotificationSpec) validate() []error {
	var errs []error

	for eventType, channels := range c.On {
		for i, tg := range channels.Telegram {
			if tg.ChatID == "" {
				errs = append(errs, fmt.Errorf("notifications.on[%q].telegram[%d].chatId is required", eventType, i))
			}

			if len(tg.BotToken.Content) == 0 {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].telegram[%d].botTokenPath contains empty token", eventType, i),
				)
			}

			if tg.ChatThreadID < 0 {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].telegram[%d].chatThreadId must be >= 0", eventType, i),
				)
			}
		}

		for i, ch := range channels.Custom {
			if ch.URL.Value.String() == "" {
				errs = append(
					errs,
					fmt.Errorf("notifications.on[%q].custom[%d].url or urlEnv is required", eventType, i),
				)
			}
		}
	}

	return errs
}
