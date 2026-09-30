package cloudsecrets

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cloudsecretspb "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager/cloudsecrets/pb"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestClientGetInfo(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	api := NewMockcontrollerAPI(ctrl)
	lastSyncAt := time.Date(2026, time.September, 29, 21, 30, 0, 0, time.UTC)
	nextSyncAt := time.Date(2026, time.September, 29, 21, 35, 0, 0, time.UTC)
	api.EXPECT().GetInfo(gomock.Any(), &cloudsecretspb.GetInfoRequest{}).Return(&cloudsecretspb.GetInfoResponse{
		Version: "v0.4.1",
		Provider: &cloudsecretspb.Provider{
			Name: "HashiCorp Vault",
			Link: "https://vault.example.com",
		},
		LastSyncAt: timestamppb.New(lastSyncAt),
		NextSyncAt: timestamppb.New(nextSyncAt),
	}, nil)

	info, err := (&Client{api: api}).GetInfo(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "v0.4.1", info.Version)
	assert.Equal(t, "HashiCorp Vault", info.ProviderName)
	assert.Equal(t, "https://vault.example.com", info.ProviderLink)
	require.NotNil(t, info.LastSyncAt)
	assert.Equal(t, lastSyncAt, *info.LastSyncAt)
	require.NotNil(t, info.NextSyncAt)
	assert.Equal(t, nextSyncAt, *info.NextSyncAt)
}

func TestClientSync(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	api := NewMockcontrollerAPI(ctrl)
	api.EXPECT().Sync(gomock.Any(), &cloudsecretspb.SyncRequest{}).Return(&cloudsecretspb.SyncResponse{
		Created:   1,
		Updated:   2,
		Removed:   3,
		Unchanged: 4,
	}, nil)

	result, err := (&Client{api: api}).Sync(context.Background())

	require.NoError(t, err)
	assert.Equal(t, SyncResult{Created: 1, Updated: 2, Removed: 3, Unchanged: 4}, result)
}
