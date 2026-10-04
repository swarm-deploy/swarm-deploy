package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestSharedObjectsDecodeComposeResourceOptions(t *testing.T) {
	var document struct {
		Secrets SharedObjects `yaml:"secrets"`
	}

	err := yaml.Unmarshal([]byte(`
secrets:
  app-secret:
    file: ./secret.txt
    driver: vault
    driver_opts:
      key: apps/demo
    template_driver: golang
    labels:
      sensitivity: high
`), &document)
	require.NoError(t, err, "decode shared object")

	secret := document.Secrets["app-secret"]
	require.NotNil(t, secret, "secret must be decoded")
	assert.Equal(t, "app-secret", secret.Alias)
	assert.Equal(t, "./secret.txt", secret.File)
	assert.Equal(t, "vault", secret.Driver, "driver YAML key must be decoded")
	assert.Equal(t, map[string]string{"key": "apps/demo"}, secret.DriverOpts)
	assert.Equal(t, "golang", secret.TemplateDriver)
	assert.Equal(t, map[string]string{"sensitivity": "high"}, secret.Labels.Map)

	rendered, err := yaml.Marshal(document)
	require.NoError(t, err, "marshal shared object")
	assert.Contains(t, string(rendered), "driver: vault")
	assert.NotContains(t, string(rendered), "drive: vault")
	assert.Contains(t, string(rendered), "driver_opts:")
	assert.Contains(t, string(rendered), "template_driver: golang")
	assert.Contains(t, string(rendered), "sensitivity: high")
}
