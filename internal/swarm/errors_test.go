package swarm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func newTestNetworkManager(t *testing.T, handler http.HandlerFunc) (NetworkManager, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+srv.Listener.Addr().String()), client.WithVersion("1.45"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	return newNetworkManager(cli), srv
}

func TestDockerErrorsAreDockerAPIErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		temp   bool
	}{
		{"internal error", http.StatusInternalServerError, false},
		{"unavailable", http.StatusServiceUnavailable, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mgr, _ := newTestNetworkManager(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"message":"fail"}`, tc.status)
			})

			_, err := mgr.List(context.Background())

			var apiErr *faults.DockerAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, tc.temp, apiErr.Temporary())
		})
	}
}

func TestDockerNotFoundKeepsSentinel(t *testing.T) {
	t.Parallel()

	mgr, _ := newTestNetworkManager(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"no such network"}`, http.StatusNotFound)
	})

	_, err := mgr.Get(context.Background(), "missing")

	require.ErrorIs(t, err, ErrNetworkNotFound)
	var apiErr *faults.DockerAPIError
	assert.NotErrorAs(t, err, &apiErr)
}

func TestDockerTimeoutIsTemporary(t *testing.T) {
	t.Parallel()

	mgr, _ := newTestNetworkManager(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := mgr.List(ctx)

	var apiErr *faults.DockerAPIError
	require.ErrorAs(t, err, &apiErr)
	var timeoutErr *faults.TimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.True(t, apiErr.Temporary())
}

func TestDockerConnectionRefusedIsUnavailable(t *testing.T) {
	t.Parallel()

	mgr, srv := newTestNetworkManager(t, func(http.ResponseWriter, *http.Request) {})
	srv.Close()

	_, err := mgr.List(context.Background())

	var apiErr *faults.DockerAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.True(t, apiErr.Temporary())
}
