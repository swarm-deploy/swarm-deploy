package secretmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager/cloudsecrets"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicestore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"go.uber.org/mock/gomock"
)

func TestServiceListKeepsUnavailableManager(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	ctrl := gomock.NewController(t)
	controller := cloudsecrets.NewMockController(ctrl)
	controller.EXPECT().GetInfo(gomock.Any()).Return(cloudsecrets.Info{}, assert.AnError)
	controller.EXPECT().Close().Return(nil)
	service.newCloudSecretsController = func(string) (cloudsecrets.Controller, error) {
		return controller, nil
	}

	managers, err := service.List(context.Background())
	require.NoError(t, err)

	require.Len(t, managers, 1)
	assert.True(t, managers[0].Controllable)
	assert.False(t, managers[0].Available)
	assert.Contains(t, managers[0].Error, assert.AnError.Error())
}

func TestServiceListReturnsControllerInfo(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	ctrl := gomock.NewController(t)
	controller := cloudsecrets.NewMockController(ctrl)
	lastSyncAt := time.Date(2026, time.September, 29, 22, 0, 0, 0, time.UTC)
	nextSyncAt := time.Date(2026, time.September, 29, 22, 5, 0, 0, time.UTC)
	controller.EXPECT().GetInfo(gomock.Any()).Return(cloudsecrets.Info{
		Version:      "v0.4.1",
		ProviderName: "HashiCorp Vault",
		ProviderLinks: cloudsecrets.Links{
			Doc:     "https://developer.hashicorp.com/vault/docs",
			Manager: "https://vault.example.com",
		},
		LastSyncAt: &lastSyncAt,
		NextSyncAt: &nextSyncAt,
	}, nil)
	controller.EXPECT().Close().Return(nil)
	service.newCloudSecretsController = func(address string) (cloudsecrets.Controller, error) {
		assert.Equal(t, "cloud-secrets:8001", address)
		return controller, nil
	}

	managers, err := service.List(context.Background())
	require.NoError(t, err)

	require.Len(t, managers, 1)
	assert.True(t, managers[0].Available)
	assert.Equal(t, "v0.4.1", managers[0].Version)
	assert.Equal(t, Provider{
		Name: "HashiCorp Vault",
		Links: ProviderLinks{
			Doc:     "https://developer.hashicorp.com/vault/docs",
			Manager: "https://vault.example.com",
		},
	}, managers[0].Provider)
	assert.Equal(t, lastSyncAt, *managers[0].LastSyncAt)
	assert.Equal(t, nextSyncAt, *managers[0].NextSyncAt)
}

func TestServiceSync(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	ctrl := gomock.NewController(t)
	controller := cloudsecrets.NewMockController(ctrl)
	controller.EXPECT().Sync(gomock.Any()).Return(cloudsecrets.SyncResult{
		Created: 1, Updated: 2, Removed: 3, Unchanged: 4,
	}, nil)
	controller.EXPECT().Close().Return(nil)
	service.newCloudSecretsController = func(string) (cloudsecrets.Controller, error) {
		return controller, nil
	}

	result, err := service.Sync(context.Background(), "infra", "cloud-secrets")

	require.NoError(t, err)
	assert.Equal(t, SyncResult{Created: 1, Updated: 2, Removed: 3, Unchanged: 4}, result)
}

func TestServiceCreate(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		request  CreateRequest
	}{
		{
			name:     "Vault KV v2",
			provider: providerHashiCorpVault,
			request: CreateRequest{
				Path:  "production/database",
				Key:   "password",
				Value: []byte("literal-value"),
			},
		},
		{
			name:     "Cloud.ru",
			provider: providerCloudRu,
			request: CreateRequest{
				Path:        "production/database/password",
				Value:       []byte("literal-value"),
				Description: "Database password",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service := newTestService(t)
			ctrl := gomock.NewController(t)
			controller := cloudsecrets.NewMockController(ctrl)
			controller.EXPECT().GetInfo(gomock.Any()).Return(cloudsecrets.Info{ProviderName: testCase.provider}, nil)
			controller.EXPECT().Create(gomock.Any(), cloudsecrets.CreateRequest{
				Path:        testCase.request.Path,
				Key:         testCase.request.Key,
				Value:       testCase.request.Value,
				Description: testCase.request.Description,
			}).Return(cloudsecrets.CreateResult{
				Path:       "normalized/path",
				ManagerURL: "https://provider.example.com/secrets/normalized/path",
			}, nil)
			controller.EXPECT().Close().Return(nil)
			service.newCloudSecretsController = func(address string) (cloudsecrets.Controller, error) {
				assert.Equal(t, "cloud-secrets:8001", address)
				return controller, nil
			}

			result, err := service.Create(context.Background(), "infra", "cloud-secrets", testCase.request)

			require.NoError(t, err)
			assert.Equal(t, CreateResult{
				Path:       "normalized/path",
				ManagerURL: "https://provider.example.com/secrets/normalized/path",
			}, result)
		})
	}
}

func TestServiceCreateRejectsProviderSpecificFields(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		request  CreateRequest
		wantErr  error
	}{
		{
			name:     "Vault requires key",
			provider: providerHashiCorpVault,
			request:  CreateRequest{Path: "production/database", Value: []byte("value")},
			wantErr:  ErrInvalidCreateRequest,
		},
		{
			name:     "Vault rejects description",
			provider: providerHashiCorpVault,
			request: CreateRequest{
				Path: "production/database", Key: "password", Value: []byte("value"), Description: "unsupported",
			},
			wantErr: ErrInvalidCreateRequest,
		},
		{
			name:     "Cloud.ru rejects key",
			provider: providerCloudRu,
			request:  CreateRequest{Path: "production/database", Key: "password", Value: []byte("value")},
			wantErr:  ErrInvalidCreateRequest,
		},
		{
			name:     "unknown provider",
			provider: "Unknown Provider",
			request:  CreateRequest{Path: "production/database", Value: []byte("value")},
			wantErr:  ErrUnsupportedProvider,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service := newTestService(t)
			ctrl := gomock.NewController(t)
			controller := cloudsecrets.NewMockController(ctrl)
			controller.EXPECT().GetInfo(gomock.Any()).Return(cloudsecrets.Info{ProviderName: testCase.provider}, nil)
			controller.EXPECT().Close().Return(nil)
			service.newCloudSecretsController = func(string) (cloudsecrets.Controller, error) { return controller, nil }

			_, err := service.Create(context.Background(), "infra", "cloud-secrets", testCase.request)

			require.Error(t, err)
			assert.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestServiceCreateUnavailable(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	service.newCloudSecretsController = func(string) (cloudsecrets.Controller, error) {
		return nil, errors.New("connection refused")
	}

	_, err := service.Create(context.Background(), "infra", "cloud-secrets", CreateRequest{
		Path: "production/database", Key: "password", Value: []byte("value"),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestServiceCreateRejectsMissingAndNonControllableManagers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service *Service
		stack   string
		wantErr error
	}{
		{
			name:    "missing manager",
			service: newTestService(t),
			stack:   "missing",
			wantErr: ErrNotFound,
		},
		{
			name: "non-controllable manager",
			service: newTestServiceWithInfo(t, model.Info{
				Metadata: metadata.Metadata{Type: stype.SecretManager},
				Name:     "cloud-secrets",
				Image:    "ghcr.io/swarm-deploy/cloud-secrets:v0.4.1",
			}),
			stack:   "infra",
			wantErr: ErrNotControllable,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testCase.service.Create(context.Background(), testCase.stack, "cloud-secrets", CreateRequest{
				Path: "production/database", Key: "password", Value: []byte("value"),
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()

	return newTestServiceWithInfo(t, model.Info{
		Metadata: metadata.Metadata{Type: stype.SecretManager},
		Name:     "cloud-secrets",
		Image:    "ghcr.io/swarm-deploy/cloud-secrets:v0.4.1",
		Environment: map[string]string{
			cloudSecretsGRPCAddressEnv: ":8001",
		},
	})
}

func newTestServiceWithInfo(t *testing.T, info model.Info) *Service {
	t.Helper()

	ctx := context.Background()
	store := servicestore.NewSQLStore(testutil.OpenSQLite(t))
	require.NoError(t, store.ReplaceStack(ctx, "infra", []model.Info{info}))

	return NewService(NewResolver(store))
}
