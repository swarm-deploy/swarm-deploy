package git

import (
	"context"
	"os"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestPushFileChangeCreatesOnlyProposalBranch(t *testing.T) {
	sourcePath, base := initSourceRepository(t)
	scratch := t.TempDir()
	spec := config.GitSpec{Repository: sourcePath, Branch: base.Name().Short()}
	branch := "core-api-enable-self-heal"

	original, err := ReadProposalFile(t.Context(), spec, scratch, "README.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(original))
	pushed, err := PushFileChange(t.Context(), spec, scratch, branch, "README.md", "update readme", "test",
		func(_ context.Context, original []byte) ([]byte, error) {
			assert.Equal(t, "hello", string(original))
			return []byte("updated"), nil
		})
	require.NoError(t, err)

	repo, err := gogit.PlainOpen(sourcePath)
	require.NoError(t, err)
	currentBase, err := repo.Reference(base.Name(), true)
	require.NoError(t, err)
	assert.Equal(t, base.Hash(), currentBase.Hash())
	proposal, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	require.NoError(t, err)
	assert.Equal(t, pushed, proposal.Hash().String())
	commit, err := repo.CommitObject(proposal.Hash())
	require.NoError(t, err)
	assert.Equal(t, []plumbing.Hash{base.Hash()}, commit.ParentHashes)
	file, err := commit.File("README.md")
	require.NoError(t, err)
	content, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t, "updated", content)
	checkouts, err := os.ReadDir(scratch)
	require.NoError(t, err)
	assert.Empty(t, checkouts)
}
