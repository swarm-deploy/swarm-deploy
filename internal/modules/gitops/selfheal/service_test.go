package selfheal

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestEnableUsesWriteBaseAndRechecksPolicy(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	tree, err := repo.Worktree()
	require.NoError(t, err)
	const filename = "stack.yaml"
	commit := func(raw string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(raw), 0o600))
		_, addErr := tree.Add(filename)
		require.NoError(t, addErr)
		_, commitErr := tree.Commit("test manifest", &gogit.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com"}})
		require.NoError(t, commitErr)
	}
	const initial = "services:\n  api: {image: app:v1}\n"
	commit(initial)
	base, err := repo.Head()
	require.NoError(t, err)
	cfg := &config.Config{Spec: config.Spec{GitOps: config.GitOpsSpec{Stacks: config.StacksSourceSpec{File: "stacks.yaml"}}}}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte("stacks:\n  - name: core\n    composeFile: stack.yaml\n"), 0o600))
	_, err = cfg.Reload(dir)
	require.NoError(t, err)
	service, err := New(cfg, false, config.GitSpec{Repository: "unused", Branch: "other", Push: &config.GitPushSpec{Repository: dir, Branch: base.Name().Short()}}, t.TempDir())
	require.NoError(t, err)
	state, err := service.Preview(t.Context(), "core", "api")
	require.NoError(t, err)
	require.Equal(t, State{Source: "global"}, state)
	published, err := service.Enable(t.Context(), "core", "api", state.Source, "test")
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, base.Hash(), head.Hash())
	proposal, err := repo.Reference(plumbing.NewBranchReferenceName(published.Branch), true)
	require.NoError(t, err)
	revision, err := repo.CommitObject(proposal.Hash())
	require.NoError(t, err)
	file, err := revision.File(filename)
	require.NoError(t, err)
	body, err := file.Contents()
	require.NoError(t, err)
	assert.Equal(t, "services:\n  api: {deploy: {labels: {"+label+": \"true\"}}, image: app:v1}\n", body)
	commit("services:\n  api: {deploy: {labels: {" + label + ": 'false'}}}\n")
	_, err = service.Enable(t.Context(), "core", "api", state.Source, "test")
	require.ErrorContains(t, err, "changed since confirmation")
	commit(body)
	already, err := service.Enable(t.Context(), "core", "api", state.Source, "test")
	require.NoError(t, err)
	assert.Equal(t, Result{AlreadyEnabled: "service"}, already)
}
