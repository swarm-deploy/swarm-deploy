package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"go.yaml.in/yaml/v3"
)

func TestNotificationChatNames(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []string
		wantErr bool
	}{
		{name: "single", yaml: "chats: deployments", want: []string{"deployments"}},
		{name: "multiple", yaml: "chats: [default, deployments]", want: []string{"default", "deployments"}},
		{name: "missing", yaml: "message: hi", want: []string{"default"}},
		{name: "empty array", yaml: "chats: []", want: []string{}},
		{name: "numeric", yaml: "chats: 42", wantErr: true},
		{name: "mapping", yaml: "chats: {foo: bar}", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rule TelegramChannel
			err := yaml.Unmarshal([]byte(tt.yaml), &rule)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, rule.ChatNames())
		})
	}
}

func TestNotificationEventRouteValidation(t *testing.T) {
	tests := []struct {
		name  string
		chats NotificationChatNames
		want  string
	}{
		{name: "default", chats: nil},
		{name: "single", chats: NotificationChatNames{"deployments"}},
		{name: "multiple", chats: NotificationChatNames{"default", "deployments"}},
		{name: "empty", chats: NotificationChatNames{}, want: "must not be empty"},
		{name: "duplicate", chats: NotificationChatNames{"default", "default"}, want: "duplicate"},
		{name: "unknown", chats: NotificationChatNames{"unknown"}, want: "unknown chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NotificationSpec{
				Messengers: NotificationMessengersSpec{Telegram: NotificationTelegramSpec{
					Chats: map[string]NotificationChatSpec{
						"default":     {ID: "-1001"},
						"deployments": {ID: "-1002"},
					},
				}},
				Events: map[events.TypeName]NotificationEventRoutes{
					events.TypeNameDeploySuccess: {Telegram: []TelegramChannel{{Chats: tt.chats}}},
				},
			}
			errs := cfg.validate()
			if tt.want == "" {
				assert.NotContains(t, joinNotificationErrors(errs), "chats")
			} else {
				assert.Contains(t, joinNotificationErrors(errs), tt.want)
			}
		})
	}
}

func joinNotificationErrors(errs []error) string {
	var result string
	for _, err := range errs {
		result += err.Error() + "\n"
	}
	return result
}

func TestNotificationRejectsOldOnKey(t *testing.T) {
	var cfg NotificationSpec
	err := yaml.Unmarshal([]byte("on:\n  DeploySuccess: {}\n"), &cfg)
	require.ErrorContains(t, err, "notifications.on is no longer supported")
}

func TestNotificationEventsUseSyncFailedInsteadOfDeployFailed(t *testing.T) {
	cfg := NotificationSpec{
		Events: map[events.TypeName]NotificationEventRoutes{
			events.TypeNameDeployFailed: {},
			events.TypeNameSyncFailed:   {},
		},
	}

	errs := joinNotificationErrors(cfg.validate())
	assert.Contains(t, errs, "DeployFailed")
	assert.NotContains(t, errs, "SyncFailed")
}
