package swarm

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockernetwork "github.com/docker/docker/api/types/network"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceAPIWarningsAreLoggedSafely(t *testing.T) {
	for _, operation := range []string{"create", "update", "registry resolution"} {
		t.Run(operation, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/distribution/") {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, err := io.WriteString(w, `{"ID":"service-id","Warnings":["ordinary warning","password=private-value"]}`)
				assert.NoError(t, err)
			}))
			defer server.Close()
			manager := newServiceManager(newTestDockerClient(t, server))
			spec := dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: "demo_api"}, TaskTemplate: dockerswarm.TaskSpec{ContainerSpec: &dockerswarm.ContainerSpec{Image: "alpine:3"}}}
			if operation == "update" {
				require.NoError(t, manager.Update(t.Context(), "service-id", dockerswarm.Version{Index: 1}, spec, dockerswarm.ServiceUpdateOptions{}))
			} else {
				_, err := manager.Create(t.Context(), spec, dockerswarm.ServiceCreateOptions{QueryRegistry: operation == "registry resolution", EncodedRegistryAuth: "never-log-this-auth"})
				require.NoError(t, err)
			}
			assert.Contains(t, output.String(), "ordinary warning")
			assert.NotContains(t, output.String(), "private-value")
			assert.NotContains(t, output.String(), "never-log-this-auth")
			if operation == "registry resolution" {
				assert.Contains(t, output.String(), "could not be accessed on a registry")
			}
		})
	}
}

func TestNetworkCreatePreservesIPAM(t *testing.T) {
	ipam := &dockernetwork.IPAM{Driver: "default", Options: map[string]string{"driver-option": "value"}, Config: []dockernetwork.IPAMConfig{{Subnet: "10.20.0.0/24", IPRange: "10.20.0.0/25", Gateway: "10.20.0.1", AuxAddress: map[string]string{"reserved": "10.20.0.2"}}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body dockernetwork.CreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, ipam, body.IPAM)
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"Id":"network-id"}`)
		assert.NoError(t, err)
	}))
	defer server.Close()
	id, err := newNetworkManager(newTestDockerClient(t, server)).Create(t.Context(), CreateNetworkRequest{Name: "demo_default", Driver: "overlay", IPAM: ipam})
	require.NoError(t, err)
	assert.Equal(t, "network-id", id)
}

func TestExternalServiceNetworkScope(t *testing.T) {
	for _, tc := range []struct {
		name, scope     string
		external, valid bool
	}{
		{"overlay", "swarm", true, true},
		{"host", "local", true, true},
		{"none", "local", true, true},
		{"bridge", "local", true, false},
		{"host", "local", false, false},
	} {
		t.Run(tc.name+tc.scope, func(t *testing.T) { assert.Equal(t, tc.valid, ServiceNetworkScope(tc.name, tc.scope, tc.external)) })
	}
}
