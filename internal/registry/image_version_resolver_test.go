package registry

import (
	"context"
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageVersionResolverResolveActualVersion(t *testing.T) {
	inspector := &fakeDistributionInspector{
		result: dockerregistry.DistributionInspect{
			Descriptor: ocispec.Descriptor{
				Digest: digest.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111"),
			},
		},
	}
	authManager := &fakeAuthManager{
		auth: "encoded-auth",
	}
	resolver := &ImageVersionResolver{
		inspector:   inspector,
		authManager: authManager,
	}

	version, err := resolver.ResolveActualVersion(context.Background(), "nginx")
	require.NoError(t, err, "resolve actual image version")

	assert.Equal(t, "docker.io/library/nginx:latest", version.Image, "unexpected normalized image")
	assert.Equal(t, "docker.io", version.Registry, "unexpected registry")
	assert.Equal(t, "library/nginx", version.Repository, "unexpected repository")
	assert.Equal(t, "latest", version.Tag, "unexpected tag")
	assert.Equal(
		t,
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		version.Digest,
		"unexpected digest",
	)
	assert.Equal(t, "docker.io/library/nginx:latest", inspector.imageRef, "unexpected image reference for inspect")
	assert.Equal(t, "encoded-auth", inspector.encodedRegistryAuth, "unexpected encoded auth")
	assert.Equal(t, "docker.io/library/nginx:latest", authManager.image, "unexpected image for auth manager")
}

func TestImageVersionResolverResolveActualVersionFailsOnInvalidImage(t *testing.T) {
	resolver := &ImageVersionResolver{
		inspector:   &fakeDistributionInspector{},
		authManager: &fakeAuthManager{},
	}

	_, err := resolver.ResolveActualVersion(context.Background(), ":::")
	require.Error(t, err, "expected parse image error")
	assert.Contains(t, err.Error(), "parse image reference", "unexpected error")
}

func TestImageVersionResolverResolveActualVersionFailsOnInspectorError(t *testing.T) {
	resolver := &ImageVersionResolver{
		inspector: &fakeDistributionInspector{
			err: assert.AnError,
		},
		authManager: &fakeAuthManager{},
	}

	_, err := resolver.ResolveActualVersion(context.Background(), "nginx")
	require.Error(t, err, "expected inspect error")
	assert.Contains(t, err.Error(), "resolve image", "unexpected error")
}

type fakeDistributionInspector struct {
	result              dockerregistry.DistributionInspect
	err                 error
	imageRef            string
	encodedRegistryAuth string
}

func (f *fakeDistributionInspector) DistributionInspect(
	_ context.Context,
	imageRef string,
	encodedRegistryAuth string,
) (dockerregistry.DistributionInspect, error) {
	f.imageRef = imageRef
	f.encodedRegistryAuth = encodedRegistryAuth

	if f.err != nil {
		return dockerregistry.DistributionInspect{}, f.err
	}

	return f.result, nil
}

type fakeAuthManager struct {
	auth  string
	err   error
	image string
}

func (f *fakeAuthManager) ResolveImage(image string) (string, error) {
	f.image = image

	if f.err != nil {
		return "", f.err
	}

	return f.auth, nil
}
