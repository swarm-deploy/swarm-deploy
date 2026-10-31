package swarm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceManagerScaling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		target      uint64
		standalone  bool
		byReference bool
	}{
		{name: "scale up", target: 5},
		{name: "scale down", target: 1},
		{name: "scale to zero", target: 0},
		{name: "no-op", target: 3},
		{name: "standalone", target: 4, standalone: true},
		{name: "assistant reference", target: 5, byReference: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			previous := uint64(3)
			stack, name, selector := "infra", "infra_api", "service-id"
			labels := map[string]string{stackNamespaceLabelKey: stack, "owner": "test"}
			if tc.standalone {
				stack, name = "", "standalone"
				delete(labels, stackNamespaceLabelKey)
			}
			if tc.byReference {
				selector = name
			}
			spec := dockerswarm.ServiceSpec{
				Annotations: dockerswarm.Annotations{Name: name, Labels: labels},
				Mode:        dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &previous}},
				TaskTemplate: dockerswarm.TaskSpec{
					ContainerSpec: &dockerswarm.ContainerSpec{Image: "api:v2", Env: []string{"KEY=value"},
						Secrets: []*dockerswarm.SecretReference{{SecretID: "secret-id", SecretName: "secret"}}},
					Placement: &dockerswarm.Placement{Constraints: []string{"node.role==worker"}},
					Networks:  []dockerswarm.NetworkAttachmentConfig{{Target: "network-id", Aliases: []string{"api"}}},
					Resources: &dockerswarm.ResourceRequirements{Limits: &dockerswarm.Limit{NanoCPUs: 1000}},
				},
				UpdateConfig:   &dockerswarm.UpdateConfig{Parallelism: 2},
				RollbackConfig: &dockerswarm.UpdateConfig{Parallelism: 1},
				EndpointSpec:   &dockerswarm.EndpointSpec{Mode: dockerswarm.ResolutionModeVIP},
			}
			updates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.Method {
				case http.MethodGet:
					assert.Equal(t, "/v1.48/services/"+selector, req.URL.Path)
					require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Service{ID: "service-id",
						Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 42}}, Spec: spec}))
				case http.MethodPost:
					updates++
					assert.Equal(t, "/v1.48/services/service-id/update", req.URL.Path)
					assert.Equal(t, "42", req.URL.Query().Get("version"))
					var actual dockerswarm.ServiceSpec
					require.NoError(t, json.NewDecoder(req.Body).Decode(&actual))
					expected := spec
					expected.Mode.Replicated = &dockerswarm.ReplicatedService{Replicas: &tc.target}
					assert.Equal(t, expected, actual, "all unrelated spec fields must survive")
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected method: %s", req.Method)
				}
			}))
			t.Cleanup(server.Close)
			manager := newServiceManager(newTestDockerClient(t, server))
			if tc.byReference {
				require.NoError(t, manager.Scale(t.Context(), NewServiceReference(stack, "api"), tc.target))
			} else {
				result, err := manager.ScaleByID(t.Context(), "service-id", tc.target)
				require.NoError(t, err)
				serviceName := "api"
				if tc.standalone {
					serviceName = name
				}
				assert.Equal(t, ServiceScaleResult{Stack: stack, Service: serviceName, PreviousReplicas: previous}, result)
			}
			if tc.target == previous {
				assert.Zero(t, updates)
			} else {
				assert.Equal(t, 1, updates)
			}
		})
	}
}

func TestServiceManagerScaleUnsupportedModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []dockerswarm.ServiceMode{
		{Global: &dockerswarm.GlobalService{}},
		{ReplicatedJob: &dockerswarm.ReplicatedJob{}},
		{GlobalJob: &dockerswarm.GlobalJob{}},
		{},
	} {
		modeName, _ := resolveServiceDeployMode(mode)
		t.Run(modeName, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, http.MethodGet, req.Method)
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Service{Spec: dockerswarm.ServiceSpec{Mode: mode}}))
			}))
			t.Cleanup(server.Close)
			_, err := newServiceManager(newTestDockerClient(t, server)).ScaleByID(t.Context(), "id", 2)
			require.ErrorIs(t, err, ErrServiceModeUnsupported)
		})
	}
}

func TestServiceManagerScaleDockerErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		inspectStatus int
		updateStatus  int
		message       string
		want          error
	}{
		{name: "inspect missing", inspectStatus: 404, want: ErrServiceNotFound},
		{name: "inspect error", inspectStatus: 500, message: "inspect unavailable"},
		{name: "removed during update", updateStatus: 404, want: ErrServiceNotFound},
		{name: "version conflict", updateStatus: 409, want: ErrServiceUpdateConflict},
		{name: "swarm sequence conflict", updateStatus: 500, message: "rpc error: code = Unknown desc = update out of sequence", want: ErrServiceUpdateConflict},
		{name: "update failure", updateStatus: 500, message: "update unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			updates := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				status := tc.inspectStatus
				if req.Method == http.MethodPost {
					updates++
					status = tc.updateStatus
				}
				if status != 0 {
					w.WriteHeader(status)
					require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"message": tc.message}))
					return
				}
				previous := uint64(1)
				require.NoError(t, json.NewEncoder(w).Encode(dockerswarm.Service{ID: "id",
					Spec: dockerswarm.ServiceSpec{Mode: dockerswarm.ServiceMode{Replicated: &dockerswarm.ReplicatedService{Replicas: &previous}}}}))
			}))
			t.Cleanup(server.Close)
			_, err := newServiceManager(newTestDockerClient(t, server)).ScaleByID(t.Context(), "id", 2)
			require.Error(t, err)
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			} else {
				assert.ErrorContains(t, err, tc.message)
			}
			if tc.inspectStatus != 0 {
				assert.Zero(t, updates)
			} else {
				assert.Equal(t, 1, updates, "conflicts must not retry stale specs")
			}
		})
	}
}
