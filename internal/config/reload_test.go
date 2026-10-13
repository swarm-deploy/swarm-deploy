package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadReportsOnlyEffectiveChanges(t *testing.T) {
	dir := t.TempDir()
	writeDesiredFiles(t, dir, "app", "backend")
	cfg := newReloadConfig()

	changed, err := cfg.Reload(dir)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = cfg.Reload(dir)
	require.NoError(t, err)
	assert.False(t, changed)

	writeDesiredFiles(t, dir, "worker", "backend")
	changed, err = cfg.Reload(dir)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "worker", cfg.Stacks()[0].Name)

	writeDesiredFiles(t, dir, "worker", "frontend")
	changed, err = cfg.Reload(dir)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "frontend", cfg.Networks()[0].Name)
}

func TestReloadFailurePreservesAcceptedStateAtomically(t *testing.T) {
	dir := t.TempDir()
	writeDesiredFiles(t, dir, "app", "backend")
	cfg := newReloadConfig()
	_, err := cfg.Reload(dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte(`
stacks:
  - name: worker
    composeFile: worker.yaml
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "networks.yaml"), []byte(`
networks:
  - name: ""
`), 0o600))

	changed, err := cfg.Reload(dir)
	require.Error(t, err)
	assert.False(t, changed)
	stacks, networks := desiredSnapshot(cfg)
	require.Len(t, stacks, 1)
	require.Len(t, networks, 1)
	assert.Equal(t, "app", stacks[0].Name)
	assert.Equal(t, "backend", networks[0].Name)
}

func TestReloadConcurrentReadsAreRaceFree(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	writeDesiredFiles(t, firstDir, "app", "backend")
	writeDesiredFiles(t, secondDir, "worker", "frontend")
	cfg := newReloadConfig()
	_, err := cfg.Reload(firstDir)
	require.NoError(t, err)

	const iterations = 100
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			for range iterations {
				stacks := cfg.Stacks()
				networks := cfg.Networks()
				if len(stacks) > 0 {
					stacks[0].Name = "mutated-reader-copy"
				}
				if len(networks) > 0 {
					networks[0].Labels["reader"] = "mutation"
				}
			}
		})
	}
	group.Go(func() {
		for i := range iterations {
			dir := firstDir
			if i%2 == 1 {
				dir = secondDir
			}
			_, reloadErr := cfg.Reload(dir)
			require.NoError(t, reloadErr)
		}
	})
	group.Wait()

	stacks := cfg.Stacks()
	networks := cfg.Networks()
	assert.NotEqual(t, "mutated-reader-copy", stacks[0].Name)
	assert.NotContains(t, networks[0].Labels, "reader")
}

func newReloadConfig() *Config {
	return &Config{Spec: Spec{
		StacksSource:   StacksSourceSpec{File: "stacks.yaml"},
		NetworksSource: NetworksSourceSpec{File: "networks.yaml"},
	}}
}

func writeDesiredFiles(t *testing.T, dir, stack, network string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stacks.yaml"), []byte("stacks:\n  - name: "+stack+
		"\n    composeFile: "+stack+".yaml\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "networks.yaml"), []byte("networks:\n  - name: "+network+
		"\n    labels:\n      team: platform\n"), 0o600))
}

func desiredSnapshot(cfg *Config) ([]StackSpec, []NetworkSpec) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cloneStacks(cfg.stacks), cloneNetworks(cfg.networks)
}
