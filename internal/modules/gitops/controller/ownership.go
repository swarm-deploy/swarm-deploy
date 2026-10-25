package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const ownershipRetryInterval = 100 * time.Millisecond

type controllerOwnership struct {
	path          string
	retryInterval time.Duration
}

type ownershipHandle struct {
	file *os.File
}

func newControllerOwnership(dataDir string) *controllerOwnership {
	return &controllerOwnership{
		path:          filepath.Join(dataDir, "swarm-deploy-controller.lock"),
		retryInterval: ownershipRetryInterval,
	}
}

func (o *controllerOwnership) acquire(ctx context.Context) (*ownershipHandle, error) {
	file, err := os.OpenFile(o.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(file.Fd()) //nolint:gosec // Unix file descriptors are int values exposed as uintptr by os.File.
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return &ownershipHandle{file: file}, nil
		case !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN):
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(o.retryInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (h *ownershipHandle) release() error {
	fd := int(h.file.Fd()) //nolint:gosec // Unix file descriptors are int values exposed as uintptr by os.File.
	unlockErr := unix.Flock(fd, unix.LOCK_UN)
	closeErr := h.file.Close()
	return errors.Join(unlockErr, closeErr)
}
