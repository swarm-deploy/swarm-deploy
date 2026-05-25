package compose

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceVolumeUnmarshalShortSyntax(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ServiceVolume
	}{
		{
			name:  "with source and mode",
			input: "/var/run/docker.sock:/var/run/docker.sock:ro",
			expected: ServiceVolume{
				Source: "/var/run/docker.sock",
				Target: "/var/run/docker.sock",
				Mode:   "ro",
			},
		},
		{
			name:  "named volume without mode",
			input: "project-data:/data",
			expected: ServiceVolume{
				Source: "project-data",
				Target: "/data",
			},
		},
		{
			name:  "anonymous volume",
			input: "/data",
			expected: ServiceVolume{
				Target: "/data",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got ServiceVolume

			err := yaml.Unmarshal([]byte(test.input), &got)
			require.NoError(t, err)

			assert.Equal(t, test.expected.Source, got.Source)
			assert.Equal(t, test.expected.Target, got.Target)
			assert.Equal(t, test.expected.Mode, got.Mode)
			assert.True(t, got.isString)
		})
	}
}

func TestServiceVolumeUnmarshalLongSyntaxAndMarshal(t *testing.T) {
	raw := `
source: /var/lib/data
target: /data
type: bind
read_only: true
bind:
  propagation: rshared
`

	var got ServiceVolume
	err := yaml.Unmarshal([]byte(raw), &got)
	require.NoError(t, err)

	assert.Equal(t, "/var/lib/data", got.Source)
	assert.Equal(t, "/data", got.Target)
	assert.False(t, got.isString)
	require.NotEmpty(t, got.asObject)

	marshaled, err := yaml.Marshal(got)
	require.NoError(t, err)
	assert.Contains(t, string(marshaled), "source: /var/lib/data")
	assert.Contains(t, string(marshaled), "target: /data")
	assert.Contains(t, string(marshaled), "type: bind")
	assert.Contains(t, string(marshaled), "read_only: true")
	assert.Contains(t, string(marshaled), "propagation: rshared")
}

func TestServiceVolumeMarshalShortSyntax(t *testing.T) {
	got, err := yaml.Marshal(ServiceVolume{
		Source:   "/var/run/docker.sock",
		Target:   "/var/run/docker.sock",
		Mode:     "ro",
		isString: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "/var/run/docker.sock:/var/run/docker.sock:ro\n", string(got))
}
