package swarm

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestManagersClassifyDockerErrors(t *testing.T) {
	operations := []struct {
		name, method, path string
		run                func(context.Context, *Swarm) error
	}{
		{"inspect service", "GET", "/services/id", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.Inspect(ctx, "id")
			return err
		}},
		{"create service", "POST", "/services/create", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.Create(ctx, dockerswarm.ServiceSpec{}, dockerswarm.ServiceCreateOptions{})
			return err
		}},
		{"update service", "POST", "/services/id/update", func(ctx context.Context, s *Swarm) error {
			return s.Services.Update(ctx, "id", dockerswarm.Version{Index: 1}, dockerswarm.ServiceSpec{}, dockerswarm.ServiceUpdateOptions{})
		}},
		{"get service", "GET", "/services/app_api", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.Get(ctx, NewServiceReference("app", "api"))
			return err
		}},
		{"list services", "GET", "/services", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.ListServices(ctx, ListServicesFilter{})
			return err
		}},
		{"list stack services", "GET", "/services", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.ListServices(ctx, ListServicesFilter{Stack: "app"})
			return err
		}},
		{"remove service", "DELETE", "/services/id", func(ctx context.Context, s *Swarm) error {
			return s.Services.Remove(ctx, "id")
		}},
		{"scale service", "POST", "/services/id/update", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.ScaleByID(ctx, "id", 2)
			return err
		}},
		{"list tasks", "GET", "/tasks", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.ListTasks(ctx, "id")
			return err
		}},
		{"get task", "GET", "/tasks/id", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.GetTask(ctx, "id")
			return err
		}},
		{"service logs", "GET", "/services/app_api/logs", func(ctx context.Context, s *Swarm) error {
			_, err := s.Services.Logs(ctx, NewServiceReference("app", "api"), ServiceLogsOptions{})
			return err
		}},
		{"task logs", "GET", "/tasks/id/logs", func(ctx context.Context, s *Swarm) error {
			_, _, err := s.Services.TaskLogs(ctx, "id", TaskLogsOptions{})
			return err
		}},
		{"list networks", "GET", "/networks", func(ctx context.Context, s *Swarm) error {
			_, err := s.Networks.List(ctx, ListNetworksFilter{})
			return err
		}},
		{"map networks", "GET", "/networks", func(ctx context.Context, s *Swarm) error {
			_, err := s.Networks.Map(ctx, []string{"id"})
			return err
		}},
		{"get network", "GET", "/networks/id", func(ctx context.Context, s *Swarm) error {
			_, err := s.Networks.Get(ctx, "id")
			return err
		}},
		{"create network", "POST", "/networks/create", func(ctx context.Context, s *Swarm) error {
			_, err := s.Networks.Create(ctx, CreateNetworkRequest{Name: "app"})
			return err
		}},
		{"list secrets", "GET", "/secrets", func(ctx context.Context, s *Swarm) error {
			_, err := s.Secrets.List(ctx, ListSecretsFilter{})
			return err
		}},
		{"create secret", "POST", "/secrets/create", func(ctx context.Context, s *Swarm) error {
			_, err := s.Secrets.Create(ctx, CreateSecretRequest{Name: "app"})
			return err
		}},
		{"inspect secret labels", "GET", "/secrets/id", func(ctx context.Context, s *Swarm) error {
			return s.Secrets.UpdateLabels(ctx, "id", map[string]string{stackNamespaceLabelKey: "app"})
		}},
		{"update secret labels", "POST", "/secrets/id/update", func(ctx context.Context, s *Swarm) error {
			return s.Secrets.UpdateLabels(ctx, "id", map[string]string{stackNamespaceLabelKey: "app"})
		}},
		{"remove secret", "DELETE", "/secrets/id", func(ctx context.Context, s *Swarm) error {
			return s.Secrets.Remove(ctx, "id")
		}},
		{"resolve secret", "GET", "/secrets/id", func(ctx context.Context, s *Swarm) error {
			_, err := s.Secrets.ResolveReference(ctx, "id", "")
			return err
		}},
		{"get config", "GET", "/configs/id", func(ctx context.Context, s *Swarm) error {
			_, err := s.Configs.Get(ctx, "id")
			return err
		}},
		{"list configs", "GET", "/configs", func(ctx context.Context, s *Swarm) error {
			_, err := s.Configs.List(ctx, ListConfigsFilter{})
			return err
		}},
		{"create config", "POST", "/configs/create", func(ctx context.Context, s *Swarm) error {
			_, err := s.Configs.Create(ctx, CreateConfigRequest{Name: "app"})
			return err
		}},
		{"remove config", "DELETE", "/configs/id", func(ctx context.Context, s *Swarm) error {
			return s.Configs.Remove(ctx, "id")
		}},
		{"list nodes", "GET", "/nodes", func(ctx context.Context, s *Swarm) error {
			_, err := s.Nodes.List(ctx)
			return err
		}},
		{"inspect node labels", "GET", "/nodes/id", func(ctx context.Context, s *Swarm) error {
			return s.Nodes.SetLabel(ctx, NodeLabelUpdateRequest{NodeID: "id", Key: "zone", Value: "a"})
		}},
		{"update node labels", "POST", "/nodes/id/update", func(ctx context.Context, s *Swarm) error {
			return s.Nodes.DeleteLabel(ctx, "id", "zone")
		}},
		{"get image", "GET", "/images/id/json", func(ctx context.Context, s *Swarm) error {
			_, err := s.Images.Get(ctx, "id")
			return err
		}},
		{"list plugins", "GET", "/plugins", func(ctx context.Context, s *Swarm) error {
			_, err := s.Plugins.List(ctx)
			return err
		}},
		{"watch events", "GET", "/events", func(ctx context.Context, s *Swarm) error {
			return s.Events.watchOnce(ctx, ResyncReasonConnected)
		}},
	}
	for _, operation := range operations {
		for _, failure := range []struct {
			name      string
			status    int
			cause     error
			temporary bool
		}{
			{"unavailable", http.StatusServiceUnavailable, cerrdefs.ErrUnavailable, true},
			{"invalid", http.StatusBadRequest, cerrdefs.ErrInvalidArgument, false},
			{"unauthorized", http.StatusUnauthorized, cerrdefs.ErrUnauthenticated, false},
			{"forbidden", http.StatusForbidden, cerrdefs.ErrPermissionDenied, false},
			{"server error", http.StatusInternalServerError, cerrdefs.ErrInternal, false},
			{"unsupported", http.StatusNotImplemented, cerrdefs.ErrNotImplemented, false},
		} {
			t.Run(operation.name+"/"+failure.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == operation.method && r.URL.Path == "/v1.48"+operation.path {
						w.WriteHeader(failure.status)
						_, _ = w.Write([]byte(`{"message":"Docker API failure"}`))
						return
					}
					// Successful inspection before an update; every failing request is checked above.
					assert.Equal(t, http.MethodGet, r.Method)
					require.NoError(t, json.MarshalWrite(w, map[string]any{
						"ID": "id", "Version": map[string]any{"Index": 1},
						"Spec": map[string]any{"Labels": map[string]string{stackNamespaceLabelKey: "app"},
							"Mode": map[string]any{"Replicated": map[string]any{"Replicas": 1}}},
					}))
				}))
				t.Cleanup(server.Close)
				err := operation.run(t.Context(), newSwarm(newTestDockerClient(t, server)))
				require.Error(t, err)
				assert.ErrorContains(t, err, "Docker API failure")
				assert.ErrorIs(t, err, failure.cause)
				var unavailable *DockerUnavailableError
				assert.Equal(t, failure.temporary, errors.As(err, &unavailable))
				var temporary faults.Error
				assert.Equal(t, failure.temporary, errors.As(err, &temporary) && temporary.Temporary())
			})
		}
	}
}

func TestImageNotFoundIsCoded(t *testing.T) {
	var coded faults.Coded = ErrImageNotFound
	assert.Equal(t, faults.CodeImageNotFound, coded.FaultCode())
	assert.Equal(t, "image not found", coded.FaultSummary())
	assert.Equal(t, "image not found", coded.Error())

	wrapped := fmt.Errorf("inspect image: %w", ErrImageNotFound)
	assert.ErrorIs(t, wrapped, ErrImageNotFound)
	require.ErrorAs(t, wrapped, &coded)
	assert.Equal(t, faults.CodeImageNotFound, coded.FaultCode())
	assert.EqualError(t, wrapped, "inspect image: image not found")
}

func TestManagersNormalizeNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"resource missing"}`))
	}))
	t.Cleanup(server.Close)
	s := newSwarm(newTestDockerClient(t, server))
	for _, tc := range []struct {
		name string
		want error
		run  func() error
	}{
		{"inspect service", ErrServiceNotFound, func() error { _, err := s.Services.Inspect(t.Context(), "id"); return err }},
		{"remove service", ErrServiceNotFound, func() error { return s.Services.Remove(t.Context(), "id") }},
		{"get task", ErrTaskNotFound, func() error { _, err := s.Services.GetTask(t.Context(), "id"); return err }},
		{"get network", ErrNetworkNotFound, func() error { _, err := s.Networks.Get(t.Context(), "id"); return err }},
		{"get image", ErrImageNotFound, func() error { _, err := s.Images.Get(t.Context(), "id"); return err }},
		{"update node", ErrNodeNotFound, func() error { return s.Nodes.DeleteLabel(t.Context(), "id", "zone") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			assert.ErrorIs(t, err, tc.want)
			assert.ErrorIs(t, err, cerrdefs.ErrNotFound)
			assert.ErrorContains(t, err, "resource missing")
		})
	}
}

func TestSecretManagerCreateConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1.48/secrets/create", req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"secret app already exists"}`))
	}))
	t.Cleanup(server.Close)
	_, err := newSecretManager(newTestDockerClient(t, server)).Create(t.Context(), CreateSecretRequest{Name: "app"})
	assert.ErrorIs(t, err, ErrSecretAlreadyExists)
	assert.ErrorIs(t, err, cerrdefs.ErrConflict)
	assert.ErrorContains(t, err, "create docker secret app: ")
	var temporary faults.Error
	assert.False(t, errors.As(err, &temporary) && temporary.Temporary())
}

func TestNodeManagerUpdateConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		status        int
		want          error
	}{
		{"conflict", "concurrent update", http.StatusConflict, ErrNodeUpdateConflict},
		{"sequence conflict", "rpc error: code = Unknown desc = update out of sequence", http.StatusInternalServerError, ErrNodeUpdateConflict},
		{"server error", "manager failed", http.StatusInternalServerError, cerrdefs.ErrInternal},
		{"removed after inspect", "node missing", http.StatusNotFound, ErrNodeNotFound},
	} {
		for _, operation := range []string{"set", "delete"} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				updates := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet {
						_, _ = w.Write([]byte(`{"ID":"id","Version":{"Index":7},"Spec":{"Labels":{"keep":"value"}}}`))
						return
					}
					updates++
					assert.Equal(t, "/v1.48/nodes/id/update", r.URL.Path)
					assert.Equal(t, "7", r.URL.Query().Get("version"))
					var spec dockerswarm.NodeSpec
					require.NoError(t, json.UnmarshalRead(r.Body, &spec))
					assert.Equal(t, "value", spec.Labels["keep"])
					w.WriteHeader(tc.status)
					require.NoError(t, json.MarshalWrite(w, map[string]string{"message": tc.message}))
				}))
				t.Cleanup(server.Close)
				m := newNodeManager(newTestDockerClient(t, server))
				var err error
				if operation == "set" {
					err = m.SetLabel(t.Context(), NodeLabelUpdateRequest{NodeID: "id", Key: "zone", Value: "a"})
				} else {
					err = m.DeleteLabel(t.Context(), "id", "zone")
				}
				assert.ErrorIs(t, err, tc.want)
				assert.ErrorContains(t, err, tc.message)
				assert.Equal(t, 1, updates, "conflicts must not retry a stale node specification")
			})
		}
	}
}
