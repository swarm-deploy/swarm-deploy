package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileLoaderLinksServiceConfigToRepositoryFile(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")
	configPath := filepath.Join(dir, "configs", "pomerium.yaml")

	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	require.NoError(t, os.WriteFile(configPath, []byte("routes: []\n"), 0o600))
	require.NoError(t, os.WriteFile(composePath, []byte(`
services:
  pomerium:
    image: pomerium/pomerium:latest
    configs:
      - source: pomerium_config
        target: /etc/pomerium/config.yaml
configs:
  pomerium_config:
    file: ./configs/pomerium.yaml
`), 0o600))

	file, err := NewFileLoader().Load(context.Background(), composePath)
	require.NoError(t, err)
	require.Len(t, file.Compose.Services, 1)
	require.Len(t, file.Compose.Services[0].Configs, 1)

	assert.Equal(t, configPath, file.Compose.Services[0].Configs[0].File)
}

func TestFileLoaderDoesNotLinkExternalConfigFile(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yaml")

	require.NoError(t, os.WriteFile(composePath, []byte(`
services:
  pomerium:
    image: pomerium/pomerium:latest
    configs:
      - source: pomerium_config
configs:
  pomerium_config:
    external: true
`), 0o600))

	file, err := NewFileLoader().Load(context.Background(), composePath)
	require.NoError(t, err)
	require.Len(t, file.Compose.Services, 1)
	require.Len(t, file.Compose.Services[0].Configs, 1)

	assert.Empty(t, file.Compose.Services[0].Configs[0].File)
}
