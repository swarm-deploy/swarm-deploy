package config

import (
	"fmt"

	"github.com/artarts36/specw"
	"github.com/artarts36/swarm-deploy/internal/event/events"
)

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
