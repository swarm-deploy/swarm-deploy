package registry

import (
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestImageVersionResolverResolveActualVersion(t *testing.T) {
	const imageDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	tests := []struct {
		name       string
		image      string
		authConfig string
		auth       dockerregistry.AuthConfig
		want       ImageVersion
	}{
		{
			name:       "default tag with Docker Hub credentials",
			image:      "nginx",
			authConfig: "{\"auths\":{\"https://index.docker.io/v1/\":{\"username\":\"robot\",\"password\":\"secret\"}}}",
			auth:       dockerregistry.AuthConfig{Username: "robot", Password: "secret", ServerAddress: "docker.io"},
			want: ImageVersion{
				Image:      "docker.io/library/nginx:latest",
				Registry:   "docker.io",
				Repository: "library/nginx",
				Tag:        "latest",
				Digest:     imageDigest,
			},
		},
		{
			name:  "digest-only reference without credentials",
			image: "registry.example/team/app@" + imageDigest,
			want: ImageVersion{
				Image:      "registry.example/team/app@" + imageDigest,
				Registry:   "registry.example",
				Repository: "team/app",
				Digest:     imageDigest,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", tt.authConfig)
			t.Setenv("DOCKER_CONFIG", t.TempDir())

			var encodedAuth string
			if tt.authConfig != "" {
				var err error
				encodedAuth, err = dockerregistry.EncodeAuthConfig(tt.auth)
				require.NoError(t, err)
			}

			ctx := t.Context()
			images := swarm.NewMockImageManager(gomock.NewController(t))
			images.EXPECT().InspectDistribution(ctx, tt.want.Image, encodedAuth).
				Return(dockerregistry.DistributionInspect{
					Descriptor: ocispec.Descriptor{Digest: digest.Digest(imageDigest)},
				}, nil)

			version, err := NewImageVersionResolver(images).ResolveActualVersion(ctx, tt.image)
			require.NoError(t, err)
			assert.Equal(t, tt.want, version)
		})
	}
}

func TestImageVersionResolverResolveActualVersionFailsBeforeInspection(t *testing.T) {
	tests := []struct {
		name       string
		image      string
		authConfig string
		wantError  string
	}{
		{name: "invalid image", image: ":::", wantError: "parse image reference"},
		{name: "invalid auth config", image: "nginx", authConfig: "{\"auths\":", wantError: "resolve image auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", tt.authConfig)
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			images := swarm.NewMockImageManager(gomock.NewController(t))

			_, err := NewImageVersionResolver(images).ResolveActualVersion(t.Context(), tt.image)
			require.ErrorContains(t, err, tt.wantError)
		})
	}
}

func TestImageVersionResolverResolveActualVersionFailsOnInspectorError(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())

	ctx := t.Context()
	images := swarm.NewMockImageManager(gomock.NewController(t))
	images.EXPECT().InspectDistribution(ctx, "docker.io/library/nginx:latest", "").
		Return(dockerregistry.DistributionInspect{}, assert.AnError)

	_, err := NewImageVersionResolver(images).ResolveActualVersion(ctx, "nginx")
	require.ErrorContains(t, err, "resolve image \"docker.io/library/nginx:latest\" in registry")
	assert.ErrorIs(t, err, assert.AnError)
}
