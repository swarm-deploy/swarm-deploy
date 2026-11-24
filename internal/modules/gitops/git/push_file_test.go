package git

import (
	"os"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestProposalFileCreatesOnlyProposalBranch(t *testing.T) {
	sourcePath, base := initSourceRepository(t)
	scratch := t.TempDir()
	spec := config.GitSpec{Repository: sourcePath, Branch: base.Name().Short()}
	branch := "core-api-enable-self-heal"

	file, err := OpenProposalFile(t.Context(), spec, scratch, "README.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", string(file.Original))
	pushed, err := file.Push(t.Context(), branch, "update readme", "test", []byte("updated"))
	file.Close(t.Context())
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
	changed, err := commit.File("README.md")
	require.NoError(t, err)
	content, err := changed.Contents()
	require.NoError(t, err)
	assert.Equal(t, "updated", content)
	checkouts, err := os.ReadDir(scratch)
	require.NoError(t, err)
	assert.Empty(t, checkouts)
}
