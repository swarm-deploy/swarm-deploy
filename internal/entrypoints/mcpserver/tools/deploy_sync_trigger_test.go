package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

func TestSyncExecute(t *testing.T) {
	control := &fakeSyncControl{queued: true}
	tool := NewSync(control)

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute deploy_sync_trigger tool")

	var payload struct {
		Queued bool `json:"queued"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	assert.True(t, payload.Queued, "expected queued=true response")
	assert.Equal(t, 1, control.called, "expected single trigger call")
}
