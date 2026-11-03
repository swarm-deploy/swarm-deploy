package preparation

import (
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

func TestLoadPinsAllComposeSourcesToTheRequestedCommit(t *testing.T) {
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{dir}})
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape")))
	require.NoError(t, os.Symlink("shared/deep", filepath.Join(dir, "inside")))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "shared", "deep"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "stacks"), 0o700))

	write := func(path, value string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(value), 0o600))
	}
	commit := func() string {
		t.Helper()
		_, addErr := worktree.Add(".")
		require.NoError(t, addErr)
		hash, commitErr := worktree.Commit("stack sources", &gogit.CommitOptions{Author: &object.Signature{Name: "test"}})
		require.NoError(t, commitErr)
		return hash.String()
	}
	manifest := `services:
  api:
    image: example/api:old
    env_file: ../inside/../api.env
configs:
  config:
    file: ../shared/config
secrets:
  token:
    file: ../shared/secret
`
	write("stacks/app.yaml", manifest)
	write("shared/api.env", "VALUE=old\n")
	write("shared/config", "old config")
	write("shared/secret", "old secret")
	write("shared/deep/keep", "keep")
	old := commit()

	write("stacks/app.yaml", "services:\n  api:\n    image: example/api:new\n")
	write("shared/api.env", "VALUE=new\n")
	write("shared/config", "new config")
	write("shared/secret", "new secret")
	for name, source := range map[string]string{
		"absolute-env":             "services:\n  api:\n    env_file: " + outside + "/data\n",
		"config-symlink-traversal": "configs:\n  app:\n    file: ../escape/../shared/config\n",
		"secret-traversal":         "secrets:\n  token:\n    file: ../../data\n",
	} {
		write("stacks/"+name+".yaml", source)
	}
	current := commit()

	repository, err := gitx.NewGoGitRepository(t.Context(), config.GitSpec{Repository: dir}, dir)
	require.NoError(t, err)
	preparer := New(&config.Config{}, repository, fs.NewLocalFileSystem(), nil)

	loaded, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "stacks/app.yaml"}, old)
	require.NoError(t, err)
	service := loaded.Compose.Services[0]
	require.Equal(t, "example/api:old", service.Image)
	require.Equal(t, "old", service.EnvFiles[0].Variables["VALUE"])
	require.Equal(t, []byte("old config"), loaded.Compose.Configs["config"].Data)
	require.Equal(t, []byte("old secret"), loaded.Compose.Secrets["token"].Data)

	for _, name := range []string{"absolute-env", "config-symlink-traversal", "secret-traversal"} {
		t.Run(name, func(t *testing.T) {
			_, err := preparer.Load(t.Context(), config.StackSpec{ComposeFile: "stacks/" + name + ".yaml"}, current)
			require.ErrorContains(t, err, "path escapes the repository")
		})
	}
}
