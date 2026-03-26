package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetActualImageVersionExecute(t *testing.T) {
	resolver := &fakeImageVersionResolver{
		version: registry.ImageVersion{
			Image:      "docker.io/library/nginx:latest",
			Registry:   "docker.io",
			Repository: "library/nginx",
			Tag:        "latest",
			Digest:     "sha256:111",
		},
	}

	tool := NewGetActualImageVersion(resolver)
	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"image": "nginx",
		},
	})
	require.NoError(t, err, "execute get_actual_image_version")
	assert.Equal(t, 1, resolver.called, "resolver must be called once")
	assert.Equal(t, "docker.io/library/nginx", resolver.image, "unexpected image passed into resolver")

	var payload struct {
		Image      string `json:"image"`
		Registry   string `json:"registry"`
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
		Digest     string `json:"digest"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "docker.io/library/nginx:latest", payload.Image, "unexpected normalized image")
	assert.Equal(t, "docker.io", payload.Registry, "unexpected registry")
	assert.Equal(t, "library/nginx", payload.Repository, "unexpected repository")
	assert.Equal(t, "latest", payload.Tag, "unexpected tag")
	assert.Equal(t, "sha256:111", payload.Digest, "unexpected digest")
}

func TestGetActualImageVersionExecuteWithDockerHubRegistry(t *testing.T) {
	resolver := &fakeImageVersionResolver{
		version: registry.ImageVersion{
			Image:      "docker.io/library/postgres:15",
			Registry:   "docker.io",
			Repository: "library/postgres",
			Tag:        "15",
			Digest:     "sha256:222",
		},
	}

	tool := NewGetActualImageVersion(resolver)
	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"image": "postgres:15",
		},
	})
	require.NoError(t, err, "execute get_actual_image_version with docker hub image")
	assert.Equal(t, "docker.io/library/postgres:15", resolver.image, "tool must force Docker Hub image reference")
}

func TestGetActualImageVersionExecuteFailsOnMissingImage(t *testing.T) {
	tool := NewGetActualImageVersion(&fakeImageVersionResolver{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{},
	})
	require.Error(t, err, "expected image required error")
	assert.Contains(t, err.Error(), "image is required", "unexpected error")
}

func TestGetActualImageVersionExecuteFailsOnNilResolver(t *testing.T) {
	tool := NewGetActualImageVersion(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"image": "nginx",
		},
	})
	require.Error(t, err, "expected missing resolver error")
	assert.Contains(t, err.Error(), "image version resolver is not configured", "unexpected error")
}

func TestGetActualImageVersionExecuteKeepsCustomRegistryImage(t *testing.T) {
	resolver := &fakeImageVersionResolver{
		version: registry.ImageVersion{
			Image:      "registry.example.com/team/api:1.2.3",
			Registry:   "registry.example.com",
			Repository: "team/api",
			Tag:        "1.2.3",
			Digest:     "sha256:333",
		},
	}
	tool := NewGetActualImageVersion(resolver)
	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"image": "registry.example.com/team/api:1.2.3",
		},
	})
	require.NoError(t, err, "execute get_actual_image_version with custom registry image")
	assert.Equal(t, "registry.example.com/team/api:1.2.3", resolver.image, "custom registry image must not be rewritten")
}
