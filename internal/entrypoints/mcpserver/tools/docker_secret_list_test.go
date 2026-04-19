package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerSecretListExecute(t *testing.T) {
	tool := NewDockerSecretList(&fakeSecretInspector{
		secrets: []swarm.Secret{
			{
				ID:     "secret-1",
				Name:   "db_password",
				Driver: "external-vault",
				Labels: map[string]string{
					"com.example.team": "platform",
				},
			},
		},
	})

	response, err := tool.Execute(context.Background(), routing.Request{})
	require.NoError(t, err, "execute docker_secret_list")

	var payload struct {
		Secrets []swarm.Secret `json:"secrets"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Secrets, 1, "expected one secret")
	assert.Equal(t, "secret-1", payload.Secrets[0].ID, "unexpected secret id")
	assert.Equal(t, "db_password", payload.Secrets[0].Name, "unexpected secret name")
	assert.Equal(t, "external-vault", payload.Secrets[0].Driver, "unexpected secret driver")
}

func TestDockerSecretListExecuteFailsOnListError(t *testing.T) {
	tool := NewDockerSecretList(&fakeSecretInspector{
		err: errors.New("docker unavailable"),
	})

	_, err := tool.Execute(context.Background(), routing.Request{})
	require.Error(t, err, "expected execute error")
	assert.Contains(t, err.Error(), "list secrets", "unexpected error")
}
