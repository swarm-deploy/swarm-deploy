package swarm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
)

func TestServiceUpdateRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "invalid published port", err: cerrdefs.ErrInvalidArgument, want: true},
		{name: "version conflict", err: ErrServiceConflict, want: true},
		{name: "missing service", err: ErrServiceNotFound, want: true},
		{name: "authorization", err: cerrdefs.ErrPermissionDenied, want: true},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "connection lost", err: errors.New("connection reset by peer")},
		{name: "server unavailable", err: cerrdefs.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ServiceUpdateRejected(fmt.Errorf("update: %w", tc.err)))
		})
	}
}

func TestServiceUpdateConflictClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		err      error
		conflict bool
	}{
		{name: "no error"},
		{name: "typed conflict", err: cerrdefs.ErrConflict, conflict: true},
		{name: "unknown sequence conflict", err: cerrdefs.ErrUnknown.WithMessage("rpc error: code = Unknown desc = update out of sequence"), conflict: true},
		{name: "wrapped sequence conflict", err: fmt.Errorf("Docker update: %w", cerrdefs.ErrUnknown.WithMessage("update out of sequence")), conflict: true},
		{name: "unrelated unknown", err: cerrdefs.ErrUnknown.WithMessage("manager unavailable")},
		{name: "not found", err: cerrdefs.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.conflict, isServiceUpdateConflict(tc.err))
		})
	}
}

func TestServiceManagerUpdateMapsSequenceConflict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, message string
		status        int
		want          error
	}{
		{name: "HTTP conflict", status: http.StatusConflict, message: "update out of sequence", want: ErrServiceConflict},
		{name: "SwarmKit unknown conflict", status: http.StatusInternalServerError, message: "rpc error: code = Unknown desc = update out of sequence", want: ErrServiceConflict},
		{name: "unrelated server error", status: http.StatusInternalServerError, message: "manager unavailable"},
		{name: "missing service", status: http.StatusNotFound, message: "no such service", want: ErrServiceNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, "/v1.48/services/service-id/update", req.URL.Path)
				assert.Equal(t, "7", req.URL.Query().Get("version"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"message":%q}`, tc.message)
			}))
			t.Cleanup(server.Close)
			manager := newServiceManager(newTestDockerClient(t, server))
			err := manager.Update(t.Context(), "service-id", dockerswarm.Version{Index: 7}, dockerswarm.ServiceSpec{
				Annotations: dockerswarm.Annotations{Name: "demo_api"},
			}, dockerswarm.ServiceUpdateOptions{})
			if tc.want != nil {
				assert.ErrorIs(t, err, tc.want)
			} else {
				assert.ErrorContains(t, err, tc.message)
				assert.NotErrorIs(t, err, ErrServiceConflict)
				assert.NotErrorIs(t, err, ErrServiceNotFound)
			}
		})
	}
}
