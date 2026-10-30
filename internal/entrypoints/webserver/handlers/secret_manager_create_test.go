package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExternalSecretCreateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		result     secretmanager.CreateResult
		managerURL string
	}{
		{
			name: "manager URL present",
			result: secretmanager.CreateResult{
				Path:       "normalized/path",
				ManagerURL: "https://provider.example.com/secrets/normalized/path",
			},
			managerURL: "https://provider.example.com/secrets/normalized/path",
		},
		{
			name:   "manager URL absent",
			result: secretmanager.CreateResult{Path: "normalized/path"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			response := externalSecretCreateResponse(testCase.result)
			payload, err := response.MarshalJSON()

			require.NoError(t, err)
			assert.Equal(t, "normalized/path", response.Path)
			managerURL, present := response.ManagerURL.Get()
			assert.Equal(t, testCase.managerURL != "", present)
			assert.Equal(t, testCase.managerURL, managerURL)
			assert.Equal(t, testCase.managerURL != "", strings.Contains(string(payload), "manager_url"))
		})
	}
}

func TestExternalSecretCreateError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "manager missing", err: secretmanager.ErrNotFound, status: 404},
		{name: "manager unsupported", err: secretmanager.ErrNotControllable, status: 409},
		{name: "provider unsupported", err: secretmanager.ErrUnsupportedProvider, status: 400},
		{name: "invalid request", err: status.Error(codes.InvalidArgument, "path is required"), status: 400},
		{name: "missing parent folder", err: status.Error(codes.FailedPrecondition, "required provider folder was not found"), status: 400},
		{name: "duplicate", err: status.Error(codes.AlreadyExists, "secret already exists"), status: 409},
		{name: "create conflict", err: status.Error(codes.Aborted, "provider changed concurrently"), status: 409},
		{name: "unavailable", err: secretmanager.ErrUnavailable, status: 503},
		{name: "deadline", err: status.Error(codes.DeadlineExceeded, "deadline exceeded"), status: 504},
		{name: "upstream internal", err: status.Error(codes.Internal, "secret creation failed"), status: 502},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			mapped := externalSecretCreateError(fmt.Errorf("create external secret: %w", testCase.err))
			require.Error(t, mapped)
			assertHTTPStatusError(t, mapped, testCase.status)
			assert.NotContains(t, mapped.Error(), "literal-secret-value")
		})
	}
}

func TestSecretManagerSyncErrorMapsConcurrentSync(t *testing.T) {
	t.Parallel()

	mapped := secretManagerSyncError(fmt.Errorf("sync cloud-secrets: %w", status.Error(codes.Aborted, "sync already in progress")))

	require.Error(t, mapped)
	assertHTTPStatusError(t, mapped, 409)
}
