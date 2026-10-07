package git

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestGoGitRepositoryReadFileReturnsIOError(t *testing.T) {
	t.Parallel()

	repo := &GoGitRepository{path: t.TempDir()}

	_, err := repo.ReadFile(context.Background(), "missing.yaml")

	var ioErr *faults.IOError
	require.ErrorAs(t, err, &ioErr)
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.False(t, ioErr.Temporary())
}
