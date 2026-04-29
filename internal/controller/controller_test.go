package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	git "github.com/swarm-deploy/swarm-deploy/internal/git"
)

func TestReloadStacksUsesRepositoryDirFirst(t *testing.T) {
	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, ".swarm-deploy")
	repoDir := filepath.Join(dataDir, "repo")
	configDir := filepath.Join(rootDir, "config")

	require.NoError(t, writeStacksFile(filepath.Join(configDir, "stacks.yaml"), "from-config"), "write config stacks")
	require.NoError(t, writeStacksFile(filepath.Join(repoDir, "stacks.yaml"), "from-repo"), "write repo stacks")

	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			StacksSource: config.StacksSourceSpec{
				File: "./stacks.yaml",
			},
		},
	}

	repository := git.NewRepository(config.GitSpec{}, filepath.Join(dataDir, "repo"))

	c := &Controller{
		cfg: cfg,
		git: repository,
	}

	loadedFrom, err := c.reloadStacks()
	require.NoError(t, err, "reload stacks")
	assert.Equal(t, filepath.Join(repoDir, "stacks.yaml"), loadedFrom, "expected path from repo")
	require.Len(t, c.cfg.Spec.Stacks, 1, "expected one stack")
	assert.Equal(t, "from-repo", c.cfg.Spec.Stacks[0].Name, "expected stack loaded from repo")
}

func TestReloadNetworksUsesRepositoryDirFirst(t *testing.T) {
	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, ".swarm-deploy")
	repoDir := filepath.Join(dataDir, "repo")
	configDir := filepath.Join(rootDir, "config")

	require.NoError(
		t,
		writeNetworksFile(filepath.Join(configDir, "networks.yaml"), "from-config"),
		"write config networks",
	)
	require.NoError(
		t,
		writeNetworksFile(filepath.Join(repoDir, "networks.yaml"), "from-repo"),
		"write repo networks",
	)

	cfg := &config.Config{
		Spec: config.Spec{
			DataDir: dataDir,
			NetworksSource: config.NetworksSourceSpec{
				File: "./networks.yaml",
			},
		},
	}

	repository := git.NewRepository(config.GitSpec{}, filepath.Join(dataDir, "repo"))

	c := &Controller{
		cfg: cfg,
		git: repository,
	}

	loadedFrom, err := c.reloadNetworks()
	require.NoError(t, err, "reload networks")
	assert.Equal(t, filepath.Join(repoDir, "networks.yaml"), loadedFrom, "expected path from repo")
	require.Len(t, c.cfg.Spec.Networks, 1, "expected one network")
	assert.Equal(t, "from-repo", c.cfg.Spec.Networks[0].Name, "expected network loaded from repo")
}

func writeStacksFile(path string, stackName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := []byte("stacks:\n  - name: " + stackName + "\n    composeFile: app.yaml\n")
	return os.WriteFile(path, content, 0o600)
}

func writeNetworksFile(path string, networkName string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := []byte("networks:\n  - name: " + networkName + "\n")
	return os.WriteFile(path, content, 0o600)
}
