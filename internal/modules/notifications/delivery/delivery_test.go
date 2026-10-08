package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	sharedfs "github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestServiceSendTracked(t *testing.T) {
	ctx := context.Background()
	live := Correlation{Receipt: Receipt{"id": "1"}}

	tests := []struct {
		name      string
		existing  bool
		sendErr   error
		wantSend  bool
		wantPut   bool
		wantError bool
	}{
		{name: "stores receipt", wantSend: true, wantPut: true},
		{name: "repeated processing is skipped", existing: true},
		{name: "send error", sendErr: errors.New("down"), wantSend: true, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store, transport := NewMockStore(ctrl), NewMockEditableTransport(ctrl)
			transport.EXPECT().ID().Return("tg").AnyTimes()
			store.EXPECT().Get(ctx, "k", "tg").Return(live, tt.existing, nil)
			if tt.wantSend {
				transport.EXPECT().Send(ctx, "text").Return(Receipt{"id": "1"}, tt.sendErr)
			}
			if tt.wantPut {
				store.EXPECT().Put(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, c Correlation) error {
					assert.Equal(t, "k", c.CorrelationKey, "key")
					assert.Equal(t, "tg", c.ChannelID, "channel")
					assert.Equal(t, DefaultTTL, c.ExpiresAt.Sub(c.CreatedAt), "default ttl")
					return nil
				})
			}

			err := NewService(store, 0).SendTracked(ctx, transport, "k", "text")
			assert.Equal(t, tt.wantError, err != nil, "error")
		})
	}
}

func TestServiceEditOrSend(t *testing.T) {
	ctx := context.Background()
	receipt := Receipt{"id": "1"}

	tests := []struct {
		name       string
		found      bool
		editErr    error
		sendErr    error
		wantEdit   bool
		wantSend   bool
		wantDelete bool
		wantError  bool
	}{
		{name: "edit succeeds", found: true, wantEdit: true, wantDelete: true},
		{name: "missing receipt sends separate message", wantSend: true, wantDelete: true},
		{name: "invalid receipt falls back to send", found: true, editErr: ErrReceiptInvalid,
			wantEdit: true, wantSend: true, wantDelete: true},
		{name: "edit error keeps correlation", found: true, editErr: errors.New("timeout"),
			wantEdit: true, wantError: true},
		{name: "send error", sendErr: errors.New("down"), wantSend: true, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store, transport := NewMockStore(ctrl), NewMockEditableTransport(ctrl)
			transport.EXPECT().ID().Return("tg").AnyTimes()
			store.EXPECT().Get(ctx, "k", "tg").Return(Correlation{CorrelationKey: "k", ChannelID: "tg", Receipt: receipt}, tt.found, nil)
			if tt.wantEdit {
				transport.EXPECT().Edit(ctx, receipt, "text").Return(tt.editErr)
			}
			if tt.wantSend {
				transport.EXPECT().Send(ctx, "text").Return(nil, tt.sendErr)
			}
			if tt.wantDelete && !tt.wantError {
				store.EXPECT().Delete(ctx, "k", "tg").Return(nil).MinTimes(1)
			}

			err := NewService(store, time.Hour).EditOrSend(ctx, transport, "k", "text")
			assert.Equal(t, tt.wantError, err != nil, "error")
		})
	}
}

func TestFileStoreTTLAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deliveries.json")
	filesystem := sharedfs.NewLocalFileSystem()
	now := time.Now()

	store, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "create store")
	store.now = func() time.Time { return now }
	require.NoError(t, store.Put(ctx, Correlation{
		CorrelationKey: "k", ChannelID: "tg", Receipt: Receipt{"id": "1"}, ExpiresAt: now.Add(time.Hour),
	}), "put")
	require.NoError(t, store.Put(ctx, Correlation{
		CorrelationKey: "k", ChannelID: "other", Receipt: Receipt{"id": "2"}, ExpiresAt: now.Add(time.Hour),
	}), "put other channel")

	reloaded, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "reload store")
	reloaded.now = func() time.Time { return now }
	got, ok, err := reloaded.Get(ctx, "k", "tg")
	require.NoError(t, err, "get")
	require.True(t, ok, "correlation must survive restart")
	assert.Equal(t, "1", got.Receipt["id"], "receipt")

	reloaded.now = func() time.Time { return now.Add(2 * time.Hour) }
	_, ok, err = reloaded.Get(ctx, "k", "tg")
	require.NoError(t, err, "get expired")
	assert.False(t, ok, "expired correlation must not be returned")

	require.NoError(t, reloaded.Delete(ctx, "k", "missing"), "deleting missing is not an error")
}

func TestFileStorePrunesExpiredOnLoad(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "deliveries.json")
	filesystem := sharedfs.NewLocalFileSystem()

	store, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "create store")
	require.NoError(t, store.Put(ctx, Correlation{
		CorrelationKey: "k", ChannelID: "tg", ExpiresAt: time.Now().Add(time.Hour),
	}), "put")

	store.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	require.NoError(t, store.Delete(ctx, "x", "y"), "noop delete")
	require.NoError(t, store.Put(ctx, Correlation{
		CorrelationKey: "n", ChannelID: "tg", ExpiresAt: time.Now().Add(3 * time.Hour),
	}), "put prunes expired")

	reloaded, err := NewFileStore(ctx, path, filesystem)
	require.NoError(t, err, "reload")
	assert.Len(t, reloaded.items, 1, "expired correlation must be removed from file")
}
