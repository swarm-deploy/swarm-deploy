package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitingControllerDoesNotDeleteActiveRenderedFiles(t *testing.T) {
	dataDir := t.TempDir()
	owner := newControllerOwnership(dataDir)
	owner.retryInterval = time.Millisecond
	handle, err := owner.acquire(t.Context())
	require.NoError(t, err)
	defer handle.release()

	renderedPath := filepath.Join(dataDir, "rendered", "app.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(renderedPath), 0o755))
	require.NoError(t, os.WriteFile(renderedPath, []byte("active deployment"), 0o600))

	waiter := newControllerOwnership(dataDir)
	waiter.retryInterval = time.Millisecond
	controller := &Controller{ownership: waiter, shutdownWait: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	_, err = os.Stat(renderedPath)
	require.NoError(t, err, "waiting controller must not clean files owned by active controller")
	cancel()
	require.NoError(t, <-done)
}

func TestControllerOwnershipSerializesInstancesAndRecoversAfterClose(t *testing.T) {
	tests := []struct {
		name         string
		releaseFirst func(*ownershipHandle) error
	}{
		{name: "clean handoff", releaseFirst: func(handle *ownershipHandle) error { return handle.release() }},
		{name: "process crash closes descriptor", releaseFirst: func(handle *ownershipHandle) error {
			return handle.file.Close()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir()
			first := newControllerOwnership(path)
			second := newControllerOwnership(path)
			first.retryInterval = time.Millisecond
			second.retryInterval = time.Millisecond
			firstHandle, err := first.acquire(t.Context())
			require.NoError(t, err)
			secondResult := make(chan *ownershipHandle, 1)
			secondError := make(chan error, 1)
			go func() {
				handle, acquireErr := second.acquire(t.Context())
				if acquireErr != nil {
					secondError <- acquireErr
					return
				}
				secondResult <- handle
			}()
			select {
			case <-secondResult:
				require.FailNow(t, "second controller acquired ownership while first was active")
			case err = <-secondError:
				require.NoError(t, err)
			case <-time.After(30 * time.Millisecond):
			}
			require.NoError(t, tt.releaseFirst(firstHandle))
			select {
			case handle := <-secondResult:
				require.NoError(t, handle.release())
			case err = <-secondError:
				require.NoError(t, err)
			case <-time.After(time.Second):
				require.FailNow(t, "second controller did not acquire released ownership")
			}
		})
	}
}

func TestControllerOwnershipWaitStopsWithContext(t *testing.T) {
	owner := newControllerOwnership(t.TempDir())
	owner.retryInterval = time.Millisecond
	handle, err := owner.acquire(t.Context())
	require.NoError(t, err)
	defer handle.release()

	waiter := newControllerOwnership(t.TempDir())
	// Point both contenders at the same lock after constructing separate instances.
	waiter.path = owner.path
	waiter.retryInterval = time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = waiter.acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
