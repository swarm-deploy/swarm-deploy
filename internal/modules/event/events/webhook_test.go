package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookReceived(t *testing.T) {
	for _, tc := range []struct {
		name   string
		queued bool
		value  string
	}{
		{name: "queued", queued: true, value: "true"},
		{name: "not queued", queued: false, value: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := &WebhookReceived{Queued: tc.queued}
			assert.Equal(t, TypeWebhookReceived, event.Type())
			assert.Equal(t, "Webhook received", event.Message())
			assert.Equal(t, map[string]string{"queued": tc.value}, event.Details())
		})
	}
}

func TestWebhookReceivedTypeRegistration(t *testing.T) {
	assert.True(t, TypeNameWebhookReceived.Valid())
	typ, ok := ParseType(string(TypeNameWebhookReceived))
	require.True(t, ok)
	assert.Equal(t, TypeWebhookReceived, typ)
	assert.Equal(t, SeverityInfo, typ.Severity())
	assert.Equal(t, CategorySync, typ.Category())
	assert.Zero(t, typ.Window(), "each webhook must remain visible")
	assert.Contains(t, Types, typ)
}
