package git

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoGitRepositoryReadFileWrapsOriginalError(t *testing.T) {
	t.Parallel()

	repo := &GoGitRepository{path: t.TempDir()}

	_, err := repo.ReadFile(context.Background(), "missing.yaml")

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), `"missing.yaml"`)
}
