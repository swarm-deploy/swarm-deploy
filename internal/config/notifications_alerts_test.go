package config

import (
	"testing"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func alertNotificationSpec() NotificationSpec {
	return NotificationSpec{Messengers: NotificationMessengersSpec{Telegram: NotificationTelegramSpec{
		BotToken: specw.File{Content: []byte("123:secret")},
		Chats:    map[string]NotificationChatSpec{"default": {ID: "-100", ThreadID: 1}, "deployments": {ID: "-101", ThreadID: 2}},
	}}}
}

func TestAlertNotificationFallback(t *testing.T) {
	spec := alertNotificationSpec()
	require.Empty(t, spec.validate())
	assert.Equal(t, []string{"default"}, (TelegramChannel{Chats: spec.AlertTelegramRoutes("deploy_failed")[0].Chats}).ChatNames())
	assert.Contains(t, spec.AlertTelegramRoutes("deploy_failed")[0].AlertOpenedTemplate("deploy_failed"), ".ResourceID")
	assert.Equal(t, "reply", spec.AlertTelegramRoutes("deploy_failed")[0].AlertResolvedAction())
	spec.Alerts = map[string]NotificationAlertRoutes{
		"default":       {Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"deployments"}, Resolved: AlertMessageSpec{Action: "edit"}}}},
		"deploy_failed": {Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"default"}, Resolved: AlertMessageSpec{Action: "reply"}}}},
	}
	require.Empty(t, spec.validate())
	assert.Equal(t, []string{"default"}, (TelegramChannel{Chats: spec.AlertTelegramRoutes("deploy_failed")[0].Chats}).ChatNames())
	assert.Equal(t, []string{"deployments"}, (TelegramChannel{Chats: spec.AlertTelegramRoutes("node_disconnected")[0].Chats}).ChatNames())
}

func TestAlertRoutesYAMLChats(t *testing.T) {
	tests := []struct {
		name, route string
		want        []string
	}{
		{"single", "chats: deployments", []string{"deployments"}},
		{"array", "chats: [default, deployments]", []string{"default", "deployments"}},
		{"missing", "opened: {message: test}", []string{"default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rule TelegramAlertChannel
			require.NoError(t, yaml.Unmarshal([]byte(tt.route), &rule))
			assert.Equal(t, tt.want, (TelegramChannel{Chats: rule.Chats}).ChatNames())
		})
	}
}

func TestAlertRouteValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*NotificationSpec)
		want   string
	}{
		{"empty chats", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{}}}}
		}, `notifications.alerts["default"].telegram[0].chats`},
		{"unknown chat", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"missing"}}}}
		}, `unknown chat`},
		{"duplicate chat", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"default", "default"}}}}
		}, `duplicate`},
		{"duplicate physical destination with identical names", func(s *NotificationSpec) {
			s.Alerts["deploy_failed"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{
				{Chats: NotificationChatNames{"default"}},
				{Chats: NotificationChatNames{"default"}},
			}}
		}, `notifications.alerts["deploy_failed"].telegram[1].chats resolves to a duplicate Telegram destination`},
		{"duplicate physical destination with different aliases", func(s *NotificationSpec) {
			s.Messengers.Telegram.Chats["duplicate"] = s.Messengers.Telegram.Chats["default"]
			s.Alerts["deploy_failed"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{
				{Chats: NotificationChatNames{"default"}},
				{Chats: NotificationChatNames{"duplicate"}},
			}}
		}, `notifications.alerts["deploy_failed"].telegram[1].chats resolves to a duplicate Telegram destination`},
		{"bad action", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Resolved: AlertMessageSpec{Action: "delete"}}}}
		}, `.resolved.action`},
		{"bad opened template", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Opened: AlertMessageSpec{Message: "{{ .alert.Title }}"}}}}
		}, `.opened.message`},
		{"bad resolved template", func(s *NotificationSpec) {
			s.Alerts["default"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{Resolved: AlertMessageSpec{Message: "{{ .event.Title }}"}}}}
		}, `.resolved.message`},
		{"unknown kind", func(s *NotificationSpec) {
			s.Alerts["wrong"] = NotificationAlertRoutes{Telegram: []TelegramAlertChannel{{}}}
		}, `unknown alert kind`},
		{"missing messenger", func(s *NotificationSpec) { s.Messengers.Telegram.BotToken.Content = nil }, `botTokenPath is required`},
		{"missing fallback chat", func(s *NotificationSpec) { delete(s.Messengers.Telegram.Chats, "default") }, `required for alert fallback`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := alertNotificationSpec()
			spec.Alerts = map[string]NotificationAlertRoutes{"default": {Telegram: []TelegramAlertChannel{{}}}}
			tt.change(&spec)
			assert.Contains(t, joinNotificationErrors(spec.validate()), tt.want)
		})
	}
}

func TestAlertRouteValidationAllowsSamePhysicalDestinationForDifferentKinds(t *testing.T) {
	spec := alertNotificationSpec()
	spec.Alerts = map[string]NotificationAlertRoutes{
		"deploy_failed":     {Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"default"}}}},
		"node_disconnected": {Telegram: []TelegramAlertChannel{{Chats: NotificationChatNames{"default"}}}},
	}

	assert.Empty(t, spec.validate())
}
