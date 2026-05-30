package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestNewCommandMarshalYAMLAsSequence(t *testing.T) {
	t.Parallel()

	command := NewCommand([]string{"/app/api", "--port", "8080"})

	raw, err := yaml.Marshal(command)
	require.NoError(t, err)

	assert.Equal(t, "- /app/api\n- --port\n- \"8080\"\n", string(raw))
}
