// Package alerts adapts alert lifecycle changes to message delivery.
package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/delivery"
)

const correlationKeyPrefix = "alert:"

const (
	openedText = `🔻 Alert opened: {{.alert.Title}}
{{.alert.ResourceType}}: {{.alert.ResourceID}}
{{.alert.Message}}`
	resolvedText = `✅ Alert resolved: {{.alert.Title}}
{{.alert.ResourceType}}: {{.alert.ResourceID}}
{{.alert.Message}}
{{.resolution}}`
)

// Channel is a delivery transport with an optional message template.
type Channel struct {
	// Transport sends messages to the channel.
	Transport delivery.Transport
	// Message is an optional text/template; it receives .alert, .resolution and .status.
	Message string
}

// Notifier converts alert lifecycle changes to messages. It implements alertmanagement.Observer.
type Notifier struct {
	mode     config.AlertNotificationMode
	channels []Channel
	delivery *delivery.Service
}

// NewNotifier creates an alert notifier.
func NewNotifier(mode config.AlertNotificationMode, channels []Channel, service *delivery.Service) *Notifier {
	return &Notifier{mode: mode, channels: channels, delivery: service}
}

// AlertOpened sends a message about the opened alert.
func (n *Notifier) AlertOpened(ctx context.Context, alert model.Alert) error {
	return n.forEachChannel(func(ch Channel) error {
		channelText, renderErr := render(ch.Message, openedText, alert, "open")
		if renderErr != nil {
			return renderErr
		}
		if n.mode == config.AlertNotificationModeEdit {
			return n.delivery.SendTracked(ctx, ch.Transport, correlationKey(alert), channelText)
		}
		_, sendErr := ch.Transport.Send(ctx, channelText)
		return sendErr
	})
}

// AlertResolved sends or edits a message about the resolved alert depending on the configured mode.
func (n *Notifier) AlertResolved(ctx context.Context, alert model.Alert) error {
	return n.forEachChannel(func(ch Channel) error {
		channelText, err := render(ch.Message, resolvedText, alert, "resolved")
		if err != nil {
			return err
		}
		if n.mode == config.AlertNotificationModeEdit {
			return n.delivery.EditOrSend(ctx, ch.Transport, correlationKey(alert), channelText)
		}
		_, err = ch.Transport.Send(ctx, channelText)
		return err
	})
}

// forEachChannel runs fn for every channel and joins errors so one broken channel does not block others.
func (n *Notifier) forEachChannel(fn func(Channel) error) error {
	var errs []error
	for _, ch := range n.channels {
		if err := fn(ch); err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", ch.Transport.ID(), err))
		}
	}
	return errors.Join(errs...)
}

// correlationKey maps an alert to the opaque key understood by delivery.
func correlationKey(alert model.Alert) string {
	return correlationKeyPrefix + alert.ID
}

func render(custom, fallback string, alert model.Alert, status string) (string, error) {
	text := strings.TrimSpace(custom)
	if text == "" {
		text = fallback
	}
	tmpl, err := template.New("alert").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse alert message template: %w", err)
	}

	resolution := ""
	if alert.Resolution != nil {
		resolution = alert.Resolution.Message
	}

	var out bytes.Buffer
	err = tmpl.Execute(&out, map[string]any{"alert": alert, "resolution": resolution, "status": status})
	if err != nil {
		return "", fmt.Errorf("render alert message template: %w", err)
	}
	return out.String(), nil
}
