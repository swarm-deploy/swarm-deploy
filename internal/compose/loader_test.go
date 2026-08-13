package compose

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoader_Load(t *testing.T) {
	cases := []struct {
		Title string
	}{
		{
			Title: "0. simple, without dependencies, service ports: mappings, labels: mappings",
		},
		{
			Title: "1. with networks, service ports: sequence, labels: sequence, networks: sequence",
		},
		{
			Title: "2. with secrets and configs, volumes: sequence, networks: mapping",
		},
	}

	for i, test := range cases {
		t.Run(test.Title, func(t *testing.T) {
			loader := NewFileLoader()

			fileRaw := []byte{}

			loader.fileReader = func(string) ([]byte, error) {
				var err error
				fileRaw, err = os.ReadFile(fmt.Sprintf("./tests/loader/%d.input.yaml", i))
				return fileRaw, err
			}

			file, err := loader.Load(fmt.Sprintf("./tests/loader/%d.input.yaml", i))
			require.NoError(t, err)

			result := bytes.NewBuffer(nil)
			encoder := yaml.NewEncoder(result)
			encoder.SetIndent(2)
			err = encoder.Encode(file.Compose)
			require.NoError(t, err)

			if string(fileRaw) != result.String() {
				err = os.WriteFile(fmt.Sprintf("./tests/loader/%d.actual.yaml", i), result.Bytes(), 0666)
				require.NoError(t, err)
			}

			assert.Equal(t, string(fileRaw), result.String())
		})
	}
}

func TestFileLoaderDigestChangesWhenSharedObjectFileContentChanges(t *testing.T) {
	tests := []struct {
		name           string
		composePayload func(objectFile string) string
		objectFile     func(dir string) string
		objectPath     func(dir string) string
	}{
		{
			name: "config file",
			composePayload: func(objectFile string) string {
				return fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    configs:
      - source: app-config
configs:
  app-config:
    file: %s
`, objectFile)
			},
			objectFile: func(string) string {
				return "./config/app.yaml"
			},
			objectPath: func(dir string) string {
				return filepath.Join(dir, "config", "app.yaml")
			},
		},
		{
			name: "secret file",
			composePayload: func(objectFile string) string {
				return fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    secrets:
      - source: app-secret
secrets:
  app-secret:
    file: %s
`, objectFile)
			},
			objectFile: func(string) string {
				return "./secrets/password.txt"
			},
			objectPath: func(dir string) string {
				return filepath.Join(dir, "secrets", "password.txt")
			},
		},
		{
			name: "absolute config file",
			composePayload: func(objectFile string) string {
				return fmt.Sprintf(`
services:
  api:
    image: nginx:latest
    configs:
      - source: app-config
configs:
  app-config:
    file: %s
`, objectFile)
			},
			objectFile: func(dir string) string {
				return filepath.Join(dir, "absolute", "config.yaml")
			},
			objectPath: func(dir string) string {
				return filepath.Join(dir, "absolute", "config.yaml")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			composePath := filepath.Join(dir, "compose.yaml")
			objectFile := tt.objectFile(dir)
			objectPath := tt.objectPath(dir)

			require.NoError(t, os.MkdirAll(filepath.Dir(objectPath), 0o755), "create object dir")
			require.NoError(t, os.WriteFile(composePath, []byte(tt.composePayload(objectFile)), 0o600), "write compose")
			require.NoError(t, os.WriteFile(objectPath, []byte("version: old\n"), 0o600), "write old object")

			loader := NewFileLoader()
			oldFile, err := loader.Load(composePath)
			require.NoError(t, err, "load compose with old object")

			require.NoError(t, os.WriteFile(objectPath, []byte("version: new\n"), 0o600), "write new object")

			newFile, err := loader.Load(composePath)
			require.NoError(t, err, "load compose with new object")

			assert.NotEqual(t, oldFile.Digest, newFile.Digest, "digest must include shared object file content")
		})
	}
}
