package compose

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeDecodingNormalizesCapabilities(t *testing.T) {
	raw := []byte(`services:
  api:
    image: alpine
    cap_add: [net_admin, CAP_NET_ADMIN, RESET]
    cap_drop: [sys_admin, net_admin, CAP_SYS_ADMIN]
`)
	tests := []struct {
		name string
		load func(t *testing.T) *Compose
	}{
		{
			name: "Parse",
			load: func(t *testing.T) *Compose {
				t.Helper()
				value, err := Parse(raw)
				require.NoError(t, err)
				return value
			},
		},
		{
			name: "FileLoader.Load",
			load: func(t *testing.T) *Compose {
				t.Helper()
				loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return raw, nil })
				file, err := loader.Load(t.Context(), "compose.yaml")
				require.NoError(t, err)
				return &file.Compose
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := testCase.load(t)
			require.Len(t, value.Services, 1)
			assert.Equal(t, []string{"CAP_NET_ADMIN"}, value.Services[0].CapAdd)
			assert.Equal(t, []string{"CAP_SYS_ADMIN"}, value.Services[0].CapDrop)
		})
	}
}

func TestNormalizeCapabilitiesHandlesAllAndReset(t *testing.T) {
	add, drop := NormalizeCapabilities([]string{"reset", "all", "net_admin"}, []string{"ALL", "sys_admin"})
	assert.Equal(t, []string{"ALL"}, add)
	assert.Empty(t, drop)
}
