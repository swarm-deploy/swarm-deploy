package compose

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestObjectRefResolution(t *testing.T) {
	t.Parallel()

	defaultMode, ownMode := DefaultObjectFileMode, os.FileMode(0o400)
	tests := []struct {
		name    string
		ref     ObjectRef
		resolve func(ObjectRef) ObjectRef
		want    ObjectRef
	}{
		{name: "service short config", ref: shortObjectRef(t, "app"), resolve: ObjectRef.ServiceConfig,
			want: ObjectRef{Source: "app", Target: "/app"}},
		{name: "service config without target", ref: ObjectRef{Source: "app", Uid: "1000"},
			resolve: ObjectRef.ServiceConfig, want: ObjectRef{Source: "app", Target: "/app", Uid: "1000"}},
		{name: "service config with relative target", ref: ObjectRef{Source: "app", Target: "app.conf"},
			resolve: ObjectRef.ServiceConfig, want: ObjectRef{Source: "app", Target: "app.conf"}},
		{name: "service short secret", ref: shortObjectRef(t, "token"), resolve: ObjectRef.ServiceSecret,
			want: ObjectRef{Source: "token", Target: "token"}},
		{name: "service secret without target", ref: ObjectRef{Source: "token"}, resolve: ObjectRef.ServiceSecret,
			want: ObjectRef{Source: "token", Target: "token"}},
		{name: "service secret keeps relative target", ref: ObjectRef{Source: "cert", Target: "tls.crt"},
			resolve: ObjectRef.ServiceSecret, want: ObjectRef{Source: "cert", Target: "tls.crt"}},
		{name: "default ownership", resolve: ObjectRef.ServiceSecret,
			ref:  ObjectRef{Source: "key", Target: "/key", Uid: "0", Gid: "0", Mode: &defaultMode},
			want: ObjectRef{Source: "key", Target: "/key"}},
		{name: "custom ownership", resolve: ObjectRef.ServiceConfig,
			ref:  ObjectRef{Source: "app", Target: "/app", Uid: "1000", Gid: "1001", Mode: &ownMode},
			want: ObjectRef{Source: "app", Target: "/app", Uid: "1000", Gid: "1001", Mode: &ownMode}},
		{name: "init job short secret", ref: shortObjectRef(t, "token"), resolve: ObjectRef.InitJobSecret,
			want: ObjectRef{Source: "token", Target: "/run/secrets/token"}},
		{name: "init job secret without target", ref: ObjectRef{Source: "key", Gid: "0"},
			resolve: ObjectRef.InitJobSecret, want: ObjectRef{Source: "key", Target: "/run/secrets/key"}},
		{name: "init job secret with relative target", ref: ObjectRef{Source: "cert", Target: "tls.crt"},
			resolve: ObjectRef.InitJobSecret, want: ObjectRef{Source: "cert", Target: "/run/secrets/tls.crt"}},
		{name: "file path is kept", ref: ObjectRef{Source: "app", File: "/repo/app.conf"},
			resolve: ObjectRef.ServiceConfig, want: ObjectRef{Source: "app", Target: "/app", File: "/repo/app.conf"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original := tc.ref
			if tc.ref.Mode != nil {
				mode := *tc.ref.Mode
				original.Mode = &mode
			}

			resolved := tc.resolve(tc.ref)

			assert.Equal(t, tc.want, resolved)
			assert.Equal(t, original, tc.ref, "resolution must not modify its receiver")
			assert.Equal(t, resolved, tc.resolve(resolved), "resolution must be idempotent")
			if resolved.Mode != nil && tc.ref.Mode != nil {
				assert.NotSame(t, tc.ref.Mode, resolved.Mode)
			}
		})
	}
}

func TestObjectRefFileOwnership(t *testing.T) {
	t.Parallel()

	mode := os.FileMode(0o400)
	tests := []struct {
		name         string
		ref          ObjectRef
		uid, gid     string
		expectedMode os.FileMode
	}{
		{name: "defaults", ref: ObjectRef{Source: "app"}, uid: "0", gid: "0", expectedMode: DefaultObjectFileMode},
		{name: "explicit", ref: ObjectRef{Source: "app", Uid: "1000", Gid: "1001", Mode: &mode},
			uid: "1000", gid: "1001", expectedMode: mode},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			uid, gid, fileMode := tc.ref.FileOwnership()

			assert.Equal(t, []any{tc.uid, tc.gid, tc.expectedMode}, []any{uid, gid, fileMode})
		})
	}
}

func shortObjectRef(t *testing.T, source string) ObjectRef {
	t.Helper()
	var ref ObjectRef
	require.NoError(t, yaml.Unmarshal([]byte(source), &ref))
	return ref
}
