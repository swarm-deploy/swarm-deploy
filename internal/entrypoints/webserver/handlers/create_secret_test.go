package handlers

import (
	"context"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerCreateSecret(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	manager := swarm.NewMockSecretManager(ctrl)
	manager.EXPECT().Create(gomock.Any(), swarm.CreateSecretRequest{
		Name:   "my-app-db-password",
		Data:   []byte("exact\nsecret\nvalue"),
		Labels: map[string]string{"description": "Database password"},
	}).Return("secret-id", nil)
	h := &handler{dockerSecrets: manager}

	response, err := h.CreateSecret(context.Background(), &generated.CreateSecretRequest{
		Name:        "my-app-db-password",
		Value:       "exact\nsecret\nvalue",
		Description: generated.NewOptString("Database password"),
	})

	require.NoError(t, err)
	assert.Equal(t, &generated.CreateSecretResponse{ID: "secret-id", Name: "my-app-db-password"}, response)
}

func TestHandlerCreateSecretValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request generated.CreateSecretRequest
	}{
		{name: "empty name", request: generated.CreateSecretRequest{Value: "value"}},
		{name: "invalid name", request: generated.CreateSecretRequest{Name: "invalid name", Value: "value"}},
		{name: "empty value", request: generated.CreateSecretRequest{Name: "valid-name"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := (&handler{}).CreateSecret(context.Background(), &testCase.request)
			require.Error(t, err)
			assertHTTPStatusError(t, err, 400)
		})
	}
}

func TestHandlerCreateSecretDuplicate(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	manager := swarm.NewMockSecretManager(ctrl)
	manager.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", errdefs.ErrConflict)
	h := &handler{dockerSecrets: manager}

	_, err := h.CreateSecret(context.Background(), &generated.CreateSecretRequest{Name: "duplicate", Value: "value"})

	require.Error(t, err)
	assertHTTPStatusError(t, err, 409)
}

func assertHTTPStatusError(t *testing.T, err error, expected int) {
	t.Helper()

	statusErr, ok := err.(*statusError)
	require.True(t, ok)
	assert.Equal(t, expected, statusErr.code)
}
