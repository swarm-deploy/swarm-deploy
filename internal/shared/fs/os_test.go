package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestOSFileSystemReturnsIOError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	filesystem := NewLocalFileSystem()
	ctx := context.Background()

	_, readErr := filesystem.ReadFile(ctx, missing)
	writeErr := filesystem.WriteFile(ctx, filepath.Join(missing, "f"), nil, 0o600)
	renameErr := filesystem.Rename(ctx, missing, filepath.Join(dir, "other"))

	for name, err := range map[string]error{"read": readErr, "write": writeErr, "rename": renameErr} {
		var ioErr *faults.IOError
		require.ErrorAs(t, err, &ioErr, name)
		assert.False(t, ioErr.Temporary(), name)
		require.ErrorIs(t, err, os.ErrNotExist, name)
	}

	require.NoError(t, filesystem.CreateDirectory(ctx, filepath.Join(dir, "a", "b"), 0o750))
}
