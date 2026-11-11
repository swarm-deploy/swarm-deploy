package swarm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockerregistry "github.com/docker/docker/api/types/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestImageManagerInspectDistribution(t *testing.T) {
	const imageRef = "registry.example/team/app:latest"
	const encodedAuth = "encoded-registry-auth"
	want := dockerregistry.DistributionInspect{
		Descriptor: ocispec.Descriptor{
			Digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		},
		Platforms: []ocispec.Platform{{OS: "linux", Architecture: "amd64"}},
	}
	tests := []struct {
		name   string
		status int
	}{
		{name: "distribution metadata", status: http.StatusOK},
		{name: "registry unauthorized", status: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, http.MethodGet, req.Method)
				assert.Equal(t, "/v1.48/distribution/"+imageRef+"/json", req.URL.Path)
				assert.Equal(t, encodedAuth, req.Header.Get(dockerregistry.AuthHeader))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				if tt.status == http.StatusOK {
					assert.NoError(t, json.NewEncoder(w).Encode(want))
					return
				}
				_, err := w.Write([]byte("{\"message\":\"unauthorized: authentication required\"}"))
				assert.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			manager := newImageManager(newTestDockerClient(t, server))

			result, err := manager.InspectDistribution(t.Context(), imageRef, encodedAuth)
			if tt.status == http.StatusOK {
				require.NoError(t, err)
				assert.Equal(t, want, result)
				return
			}
			require.ErrorContains(t, err, "unauthorized: authentication required")
			assert.True(t, cerrdefs.IsUnauthorized(err))
			var fault faults.Coded
			require.ErrorAs(t, err, &fault)
			assert.Equal(t, faults.CodeImagePullDenied, fault.FaultCode())
			assert.Empty(t, result)
		})
	}
}
