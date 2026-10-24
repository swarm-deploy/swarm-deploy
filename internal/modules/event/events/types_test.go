package events

import (
	"encoding/json"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventTypeIdentifiersUsePascalCase(t *testing.T) {
	seen := make(map[TypeName]bool, len(Types))
	for _, typ := range Types {
		name := typ.Name()
		first, _ := utf8.DecodeRuneInString(string(name))
		assert.Truef(t, unicode.IsUpper(first), "event type %q must start with an uppercase letter", name)
		assert.NotContains(t, string(name), "_")
		assert.False(t, seen[name], "event type %q is duplicated", name)
		seen[name] = true
	}

	assert.Equal(t, map[TypeName]bool{
		TypeNameServiceCatalogUpdated:            true,
		TypeNameDeploySuccess:                    true,
		TypeNameAlertOpened:                      true,
		TypeNameAlertResolved:                    true,
		TypeNameDeployFailed:                     true,
		TypeNameConfigurationChanged:             true,
		TypeNameDeployPreparationFailed:          true,
		TypeNameDeployInterrupted:                true,
		TypeNameGitPullFailed:                    true,
		TypeNameGitPullSucceeded:                 true,
		TypeNameOutboxDeliveryFailed:             true,
		TypeNameOutboxDeliveryRecovered:          true,
		TypeNameOutboxDeliveryDiscarded:          true,
		TypeNameSyncManualStarted:                true,
		TypeNameWebhookReceived:                  true,
		TypeNameNodeJoined:                       true,
		TypeNameNodeConnected:                    true,
		TypeNameNodeDisconnected:                 true,
		TypeNameServiceMissed:                    true,
		TypeNameServiceReplicasIncreased:         true,
		TypeNameServiceReplicasDecreased:         true,
		TypeNameServiceRestarted:                 true,
		TypeNameServicePruned:                    true,
		TypeNameNetworkCreated:                   true,
		TypeNameUserAuthenticated:                true,
		TypeNameAssistantPromptInjectionDetected: true,
	}, seen)
}

func TestEventTypeClassification(t *testing.T) {
	internal := map[TypeName]bool{
		TypeNameAlertOpened: true, TypeNameAlertResolved: true,
		TypeNameOutboxDeliveryFailed: true, TypeNameOutboxDeliveryRecovered: true,
		TypeNameOutboxDeliveryDiscarded: true,
		TypeNameGitPullFailed:           true, TypeNameGitPullSucceeded: true,
		TypeNameConfigurationChanged: true, TypeNameDeployFailed: true,
		TypeNameDeployPreparationFailed: true, TypeNameDeployInterrupted: true,
	}
	for _, typ := range Types {
		t.Run(typ.String(), func(t *testing.T) {
			parsed, ok := ParseType(typ.String())
			assert.True(t, ok)
			assert.Equal(t, typ, parsed)
			assert.Equal(t, !internal[typ.Name()], typ.IsPublic())
			assert.Equal(t, typ.IsPublic(), typ.Name().Valid())
			data, err := json.Marshal(typ)
			require.NoError(t, err)
			var restored Type
			require.NoError(t, json.Unmarshal(data, &restored))
			assert.Equal(t, typ, restored)
		})
	}
	_, ok := ParseType("Unknown")
	assert.False(t, ok)
	assert.False(t, TypeName("Unknown").Valid())
	assert.False(t, Type{}.IsPublic())

	legacy, ok := ParseType("SendNotificationFailed")
	require.True(t, ok, "historical Event History types must remain readable")
	assert.Equal(t, TypeName("SendNotificationFailed"), legacy.Name())
	assert.Equal(t, SeverityError, legacy.Severity())
	assert.Equal(t, CategorySync, legacy.Category())
	assert.False(t, legacy.IsPublic())
	assert.False(t, TypeName("SendNotificationFailed").Valid(), "legacy type must not be configurable")
	assert.NotContains(t, Types, legacy, "legacy type must not return to runtime registrations")
	_, ok = ParseRuntimeType("SendNotificationFailed")
	assert.False(t, ok, "legacy type must not return to runtime dispatch")
}
