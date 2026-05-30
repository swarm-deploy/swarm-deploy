package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"github.com/swarm-deploy/swarm-deploy/internal/serviceupdater"
)

func TestServiceImageUpdateExecute(t *testing.T) {
	updater := &fakeServiceUpdater{
		result: serviceupdater.UpdateImageVersionResult{
			StackName:       "core",
			ServiceName:     "api",
			OldImage:        "ghcr.io/acme/api:1.0.0",
			NewImage:        "ghcr.io/acme/api:2.0.0",
			BranchName:      "api-up-image-2.0.0",
			BranchURL:       "https://github.com/acme/repo/tree/api-up-image-2.0.0",
			CommitHash:      "abc123",
			MergeRequestURL: "https://github.com/acme/repo/pull/1",
		},
	}

	tool := NewServiceImageUpdate(updater)

	ctx := security.ContextWithUser(context.Background(), security.User{Name: "artem"})
	response, err := tool.Execute(ctx, routing.Request{
		Payload: map[string]any{
			"stack":        "core",
			"service":      "api",
			"imageVersion": "2.0.0",
			"reason":       "please update",
		},
	})
	require.NoError(t, err, "execute service_image_update")
	assert.Equal(t, 1, updater.called, "updater should be called once")
	assert.Equal(t, "core", updater.input.StackName, "unexpected stack argument")
	assert.Equal(t, "api", updater.input.ServiceName, "unexpected service argument")
	assert.Equal(t, "2.0.0", updater.input.ImageVersion, "unexpected imageVersion argument")
	assert.Equal(t, "please update", updater.input.Reason, "unexpected reason argument")
	assert.Equal(t, "artem", updater.input.UserName, "unexpected user name")

	var payload serviceupdater.UpdateImageVersionResult
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")
	assert.Equal(t, updater.result, payload, "unexpected payload")
}

func TestServiceImageUpdateExecuteUsesUnknownUserWhenContextMissing(t *testing.T) {
	updater := &fakeServiceUpdater{
		result: serviceupdater.UpdateImageVersionResult{
			StackName:   "core",
			ServiceName: "api",
		},
	}

	tool := NewServiceImageUpdate(updater)
	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":        "core",
			"service":      "api",
			"imageVersion": "2.0.0",
			"reason":       "please update",
		},
	})
	require.NoError(t, err, "execute service_image_update")
	assert.Equal(t, "unknown-user", updater.input.UserName, "tool should use unknown-user by default")
}

func TestServiceImageUpdateExecuteFailsOnMissingFields(t *testing.T) {
	tool := NewServiceImageUpdate(&fakeServiceUpdater{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack": "core",
		},
	})
	require.Error(t, err, "missing fields should fail")
	assert.Contains(t, err.Error(), "service is required", "unexpected error")
}

func TestServiceImageUpdateExecuteFailsOnUpdaterError(t *testing.T) {
	tool := NewServiceImageUpdate(&fakeServiceUpdater{
		err: errors.New("failed"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":        "core",
			"service":      "api",
			"imageVersion": "2.0.0",
			"reason":       "please update",
		},
	})
	require.Error(t, err, "updater error must fail")
	assert.Contains(t, err.Error(), "failed", "unexpected error")
}

func TestServiceImageUpdateExecuteFailsOnNilUpdater(t *testing.T) {
	tool := NewServiceImageUpdate(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"stack":        "core",
			"service":      "api",
			"imageVersion": "2.0.0",
			"reason":       "please update",
		},
	})
	require.Error(t, err, "nil updater must fail")
	assert.Contains(t, err.Error(), "service updater is not configured", "unexpected error")
}
