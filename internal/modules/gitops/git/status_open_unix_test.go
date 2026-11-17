//go:build linux || darwin

package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"golang.org/x/sys/unix"
)

func TestLocalStatusRejectsNamedPipesBeforeReading(t *testing.T) {
	for _, tc := range []struct {
		path    string
		symlink bool
	}{
		{path: ".gitignore"},
		{path: ".gitignore", symlink: true},
		{path: ".gitmodules"},
		{path: ".git/info/exclude"},
	} {
		name := tc.path
		if tc.symlink {
			name += " symlink"
		}
		t.Run(name, func(t *testing.T) {
			dir, _ := initSourceRepository(t)
			path := filepath.Join(dir, tc.path)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			fifo := path
			if tc.symlink {
				fifo = filepath.Join(dir, "fifo")
				require.NoError(t, os.Symlink(filepath.Base(fifo), path))
			}
			require.NoError(t, unix.Mkfifo(fifo, 0o600))
			repository := NewLazyProxy(config.GitSpec{}, dir)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := repository.Status(ctx, 100)
				done <- err
			}()
			select {
			case err := <-done:
				require.ErrorIs(t, err, errStatusSpecialFile)
			case <-ctx.Done():
				// Unblock a regressed open so it cannot leave a goroutine behind.
				fd, err := unix.Open(fifo, unix.O_RDWR|unix.O_NONBLOCK, 0)
				require.NoError(t, err)
				defer unix.Close(fd)
				t.Fatal("status did not reject the named pipe before the deadline")
			}
		})
	}
}
