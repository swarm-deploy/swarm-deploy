package swarm

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceManagerRestart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		mode          dockerswarm.ServiceMode
		zero          bool
		standalone    bool
		inspectStatus int
		stopStatus    int
		restoreStatus int
		changed       bool
		want          error
		updates       int
		inspections   int
	}{
		{name: "success", updates: 2, inspections: 2},
		{name: "standalone by ID", standalone: true, updates: 2, inspections: 2},
		{name: "global", mode: dockerswarm.ServiceMode{Global: &dockerswarm.GlobalService{}}, want: ErrServiceModeUnsupported, inspections: 1},
		{name: "replicated job", mode: dockerswarm.ServiceMode{ReplicatedJob: &dockerswarm.ReplicatedJob{}}, want: ErrServiceModeUnsupported, inspections: 1},
		{name: "global job", mode: dockerswarm.ServiceMode{GlobalJob: &dockerswarm.GlobalJob{}}, want: ErrServiceModeUnsupported, inspections: 1},
		{name: "zero", zero: true, want: ErrServiceZeroReplicas, inspections: 1},
		{name: "missing", inspectStatus: 404, want: ErrServiceNotFound, inspections: 1},
		{name: "version conflict", stopStatus: 409, want: ErrServiceUpdateConflict, updates: 1, inspections: 1},
		{name: "docker stop failure", stopStatus: 500, updates: 1, inspections: 1},
		{name: "restore version conflict", restoreStatus: 409, want: ErrServiceUpdateConflict, updates: 4, inspections: 4},
		{name: "restore failure leaves zero", restoreStatus: 500, updates: 4, inspections: 4},
		{name: "restore service removed", restoreStatus: 404, want: ErrServiceNotFound, updates: 2, inspections: 2},
		{name: "concurrent change after stop", changed: true, want: ErrServiceUpdateConflict, updates: 1, inspections: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			replicas := uint64(3)
			if tc.zero {
				replicas = 0
			}
			mode := tc.mode
			if mode == (dockerswarm.ServiceMode{}) {
				mode.Replicated = &dockerswarm.ReplicatedService{Replicas: &replicas}
			}
			labels := map[string]string{"com.docker.stack.namespace": "infra"}
			name := "infra_api"
			if tc.standalone {
				labels, name = nil, "api"
			}
			service := dockerswarm.Service{ID: "service-id", Meta: dockerswarm.Meta{Version: dockerswarm.Version{Index: 42}}, Spec: dockerswarm.ServiceSpec{Annotations: dockerswarm.Annotations{Name: name, Labels: labels}, Mode: mode}}
			updates := 0
			inspections := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					inspections++
					assert.Equal(t, "/v1.48/services/service-id", r.URL.Path)
					if tc.inspectStatus != 0 {
						w.WriteHeader(tc.inspectStatus)
						_, _ = w.Write([]byte(`{"message":"Docker detail"}`))
						return
					}
					actual := service
					if tc.changed && updates > 0 {
						actual.Spec.Name = "changed"
					}
					require.NoError(t, json.MarshalWrite(w, actual, jsonv1.DefaultOptionsV1()))
					return
				}
				require.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1.48/services/service-id/update", r.URL.Path)
				updates++
				status := tc.restoreStatus
				if updates == 1 {
					status = tc.stopStatus
				}
				if status != 0 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"Docker detail"}`))
					return
				}
				var spec dockerswarm.ServiceSpec
				require.NoError(t, json.UnmarshalRead(r.Body, &spec, jsonv1.DefaultOptionsV1()))
				if updates == 1 {
					assert.Zero(t, *spec.Mode.Replicated.Replicas)
				} else {
					assert.Equal(t, uint64(3), *spec.Mode.Replicated.Replicas)
				}
				service.Spec = spec
				service.Version.Index++
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(server.Close)
			manager := newServiceManager(newTestDockerClient(t, server))
			count, err := manager.restart(t.Context(), NewServiceIDReference("service-id"), time.Millisecond)
			failed := tc.want != nil || tc.stopStatus != 0 || tc.restoreStatus != 0
			if failed {
				require.Error(t, err)
				assert.Zero(t, count)
			} else {
				require.NoError(t, err)
				assert.Equal(t, uint64(3), count)
			}
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			}
			assert.Equal(t, tc.updates, updates)
			assert.Equal(t, tc.inspections, inspections)
			if tc.restoreStatus != 0 {
				assert.Zero(t, *service.Spec.Mode.Replicated.Replicas, "failed restoration leaves service stopped")
			}
		})
	}
}
