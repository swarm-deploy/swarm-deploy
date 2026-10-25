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
	require.Contains(t, file.Compose.Configs, "pomerium_config")
	assert.Equal(t, []byte("routes: []\n"), file.Compose.Configs["pomerium_config"].Data)
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
	assert.Nil(t, file.Compose.Configs["pomerium_config"].Data)
}

func TestFileLoaderDistinguishesEmptyConfigFileFromExternalConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectedNil bool
	}{
		{
			name: "empty file",
			config: `
configs:
  app_config:
    file: ./empty.yaml
`,
		},
		{
			name: "external config",
			config: `
configs:
  app_config:
    external: true
`,
			expectedNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composePath := filepath.Join("repo", "compose.yaml")
			loader := NewFileLoaderWithReader(func(_ context.Context, path string) ([]byte, error) {
				switch path {
				case composePath:
					return []byte(tt.config), nil
				case filepath.Join("repo", "empty.yaml"):
					return nil, nil
				default:
					return nil, os.ErrNotExist
				}
			})

			file, err := loader.Load(context.Background(), composePath)
			require.NoError(t, err)

			data := file.Compose.Configs["app_config"].Data
			assert.Empty(t, data)
			if tt.expectedNil {
				assert.Nil(t, data)
			} else {
				assert.NotNil(t, data)
			}
		})
	}
}
