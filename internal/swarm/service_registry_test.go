package swarm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerregistry "github.com/docker/docker/api/types/registry"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the pinned SDK over its Engine API transport: a digest still needs
// DistributionInspect to populate TaskTemplate.Placement.Platforms.
func TestServiceManagerResolvesDigestPinnedPlatforms(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			image := "registry.example/team/api@sha256:" + strings.Repeat("a", 64)
			platforms := []dockerswarm.Platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}}
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				assert.Equal(t, "test-registry-auth", req.Header.Get(dockerregistry.AuthHeader))
				w.Header().Set("Content-Type", "application/json")
				if req.Method == http.MethodGet && req.URL.Path == "/v1.48/distribution/"+image+"/json" {
					require.NoError(t, json.NewEncoder(w).Encode(dockerregistry.DistributionInspect{
						Descriptor: ocispec.Descriptor{Digest: digest.Digest("sha256:" + strings.Repeat("a", 64))},
						Platforms:  []ocispec.Platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}},
					}))
					return
				}
				if req.Method != http.MethodPost {
					http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
					return
				}
				var spec dockerswarm.ServiceSpec
				require.NoError(t, json.NewDecoder(req.Body).Decode(&spec))
				assert.Equal(t, image, spec.TaskTemplate.ContainerSpec.Image)
				require.NotNil(t, spec.TaskTemplate.Placement)
				assert.Equal(t, platforms, spec.TaskTemplate.Placement.Platforms)
				assert.Equal(t, []string{"node.labels.pool==api"}, spec.TaskTemplate.Placement.Constraints)
				if operation == "create" {
					require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.ServiceCreateResponse{ID: "api-id"}))
				} else {
					assert.Equal(t, "7", req.URL.Query().Get("version"))
					require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.ServiceUpdateResponse{}))
				}
			}))
			t.Cleanup(server.Close)
			manager := newServiceManager(newTestDockerClient(t, server))
			spec := dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
				ContainerSpec: &dockerswarm.ContainerSpec{Image: image},
				Placement:     &dockerswarm.Placement{Constraints: []string{"node.labels.pool==api"}},
			}}
			target := "/v1.48/services/create"
			if operation == "create" {
				id, err := manager.Create(t.Context(), spec, dockerswarm.ServiceCreateOptions{QueryRegistry: true, EncodedRegistryAuth: "test-registry-auth"})
				require.NoError(t, err)
				assert.Equal(t, "api-id", id)
			} else {
				target = "/v1.48/services/api-id/update"
				require.NoError(t, manager.Update(t.Context(), "api-id", dockerswarm.Version{Index: 7}, spec,
					dockerswarm.ServiceUpdateOptions{QueryRegistry: true, EncodedRegistryAuth: "test-registry-auth"}))
			}
			assert.Equal(t, []string{"GET /v1.48/distribution/" + image + "/json", "POST " + target}, requests)
		})
	}
}
