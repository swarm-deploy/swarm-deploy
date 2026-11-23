package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager"
	secretmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
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

func TestResolveSynchronizedSecret(t *testing.T) {
	t.Parallel()

	managedParent := func(id string, name string, externalPath string) swarm.Secret {
		return swarm.Secret{
			ID:   id,
			Name: name,
			Labels: map[string]string{
				secretmodel.ManagedLabel:      "true",
				secretmodel.LogicalPathLabel:  name,
				secretmodel.ExternalPathLabel: externalPath,
			},
		}
	}
	tests := []struct {
		name        string
		createdPath string
		createdKey  string
		secrets     []swarm.Secret
		want        swarm.Secret
		wantFound   bool
	}{
		{
			name:        "Vault key matches exact external path",
			createdPath: "prod/database",
			createdKey:  "password",
			secrets:     []swarm.Secret{managedParent("vault-id", "prod-database-password", "prod/database/password")},
			want:        managedParent("vault-id", "prod-database-password", "prod/database/password"),
			wantFound:   true,
		},
		{
			name:        "Cloud.ru path matches configured root suffix",
			createdPath: "api/database/password",
			secrets:     []swarm.Secret{managedParent("cloudru-id", "api-database-password", "prod/apps/api/database/password")},
			want:        managedParent("cloudru-id", "api-database-password", "prod/apps/api/database/password"),
			wantFound:   true,
		},
		{
			name:        "version resource is ignored in favor of logical parent",
			createdPath: "prod/database",
			createdKey:  "password",
			secrets: []swarm.Secret{
				managedParent("parent-id", "prod-database-password", "prod/database/password"),
				{
					ID:   "version-id",
					Name: "prod-database-password-v2",
					Labels: map[string]string{
						secretmodel.ManagedLabel:      "true",
						secretmodel.LogicalPathLabel:  "prod-database-password",
						secretmodel.ExternalPathLabel: "prod/database/password",
					},
				},
			},
			want:      managedParent("parent-id", "prod-database-password", "prod/database/password"),
			wantFound: true,
		},
		{
			name:        "ambiguous root suffix is not resolved",
			createdPath: "api/password",
			secrets: []swarm.Secret{
				managedParent("one", "api-password", "prod/api/password"),
				managedParent("two", "api-password-2", "staging/api/password"),
			},
		},
		{
			name:        "unmanaged secret is not resolved",
			createdPath: "api/password",
			secrets: []swarm.Secret{{
				ID:   "unmanaged",
				Name: "api-password",
				Labels: map[string]string{
					secretmodel.LogicalPathLabel:  "api-password",
					secretmodel.ExternalPathLabel: "api/password",
				},
			}},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			manager := swarm.NewMockSecretManager(gomock.NewController(t))
			manager.EXPECT().List(gomock.Any(), swarm.ListSecretsFilter{
				Labels: map[string]string{secretmodel.ManagedLabel: "true"},
			}).Return(testCase.secrets, nil)

			got, found, err := resolveSynchronizedSecret(
				context.Background(),
				manager,
				testCase.createdPath,
				testCase.createdKey,
			)

			require.NoError(t, err)
			assert.Equal(t, testCase.wantFound, found)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSecretManagerSyncResponseDoesNotExposeSecretValue(t *testing.T) {
	t.Parallel()

	response := generated.SecretManagerSyncResponse{
		Created: 1,
		Secret: generated.NewOptSynchronizedSecretIdentity(generated.SynchronizedSecretIdentity{
			ID:   "secret-id",
			Name: "actual-secret-name",
		}),
	}

	payload, err := response.MarshalJSON()

	require.NoError(t, err)
	assert.JSONEq(t, `{"created":1,"updated":0,"removed":0,"unchanged":0,"secret":{"id":"secret-id","name":"actual-secret-name"}}`, string(payload))
	assert.NotContains(t, string(payload), "value")
}
