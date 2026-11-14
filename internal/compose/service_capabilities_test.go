package compose

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestComposeDecodesCanonicalCapabilities(t *testing.T) {
	raw := []byte(`x-defaults: &defaults
  cap_add: [SYS_ADMIN]
  cap_drop: [NET_ADMIN, sys_admin, CAP_SYS_ADMIN]
services:
  app:
    <<: *defaults
    image: alpine
    cap_add: [net_admin, CAP_NET_ADMIN, RESET, ' sys_ptrace ']
`)
	tests := []struct {
		name string
		load func(*testing.T) Compose
	}{
		{"Parse", func(t *testing.T) Compose {
			t.Helper()
			parsed, err := Parse(raw)
			require.NoError(t, err)
			return *parsed
		}},
		{"Load", func(t *testing.T) Compose {
			t.Helper()
			loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return raw, nil })
			file, err := loader.Load(t.Context(), "compose.yaml")
			require.NoError(t, err)
			return file.Compose
		}},
	}

	want := ServiceCapabilities{CapAdd: []string{"CAP_NET_ADMIN", "CAP_SYS_PTRACE"}, CapDrop: []string{"CAP_SYS_ADMIN"}}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			desired := testCase.load(t)
			require.Len(t, desired.Services, 1)
			assert.Equal(t, "alpine", desired.Services[0].Image)
			assert.Equal(t, want, desired.Services[0].Capabilities)

			rendered, err := yaml.Marshal(desired)
			require.NoError(t, err)
			assert.YAMLEq(t, `services:
  app:
    image: alpine
    cap_add: [CAP_NET_ADMIN, CAP_SYS_PTRACE]
    cap_drop: [CAP_SYS_ADMIN]
`, string(rendered))
		})
	}
}

func TestServiceCapabilitiesNormalizeAll(t *testing.T) {
	tests := []struct {
		name string
		in   ServiceCapabilities
		want ServiceCapabilities
	}{
		{
			name: "keep individual additions with drop all",
			in:   ServiceCapabilities{CapAdd: []string{"net_admin"}, CapDrop: []string{"all", "sys_admin"}},
			want: ServiceCapabilities{CapAdd: []string{"CAP_NET_ADMIN"}, CapDrop: []string{"ALL"}},
		},
		{
			name: "add all wins over drop all",
			in:   ServiceCapabilities{CapAdd: []string{"reset", "all", "net_admin"}, CapDrop: []string{"ALL"}},
			want: ServiceCapabilities{CapAdd: []string{"ALL"}},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.in.normalize()
			assert.Equal(t, testCase.want.CapAdd, got.CapAdd)
			assert.ElementsMatch(t, testCase.want.CapDrop, got.CapDrop)
		})
	}
}

func TestServiceCapabilitiesPreserveFlatSnapshotJSON(t *testing.T) {
	raw := []byte(`{"name":"app","image":"alpine","cap_add":["net_admin"],"cap_drop":["ALL"]}`)
	var service Service
	require.NoError(t, json.Unmarshal(raw, &service))
	assert.Equal(t, "app", service.Name)
	assert.Equal(t, "alpine", service.Image)
	assert.Equal(t, []string{"net_admin"}, service.Capabilities.CapAdd)
	assert.Equal(t, []string{"ALL"}, service.Capabilities.CapDrop)

	encoded, err := json.Marshal(service)
	require.NoError(t, err)
	var fields map[string]jsontext.Value
	require.NoError(t, json.Unmarshal(encoded, &fields))
	assert.JSONEq(t, `["net_admin"]`, string(fields["cap_add"]))
	assert.JSONEq(t, `["ALL"]`, string(fields["cap_drop"]))
	assert.NotContains(t, fields, "Capabilities")
}
