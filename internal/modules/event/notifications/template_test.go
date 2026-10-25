package notifications

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	notificationtemplate "github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/template"
)

func TestDefaultTemplate(t *testing.T) {
	renderer, err := notificationtemplate.New(DefaultTemplate())
	require.NoError(t, err)
	payload := NodeJoinedReferencePayload()
	actual, err := renderer.Render(payload)
	require.NoError(t, err)
	assert.Equal(t, payload.Message, actual)
}
