package deployments

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestInitJobCreationFailureRetryEligibilityPersistsAcrossRestart(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		status                                 int
		retryable                              bool
		dropConnection, closeServer, tlsServer bool
	}{
		{name: "unavailable", status: http.StatusServiceUnavailable, retryable: true},
		{name: "invalid", status: http.StatusBadRequest},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "dropped connection", dropConnection: true, retryable: true},
		{name: "connection refused", closeServer: true, retryable: true},
		{name: "invalid TLS", tlsServer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DOCKER_AUTH_CONFIG", "")
			t.Setenv("DOCKER_CONFIG", t.TempDir())
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1.48/services/create", r.URL.Path)
				if tc.dropConnection {
					_, readErr := io.Copy(io.Discard, r.Body)
					require.NoError(t, readErr)
					conn, _, hijackErr := http.NewResponseController(w).Hijack()
					require.NoError(t, hijackErr)
					require.NoError(t, conn.Close())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"Docker API failure"}`))
			}))
			scheme := "http"
			if tc.tlsServer {
				server.StartTLS()
				scheme = "https"
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			dockerClient, err := client.NewClientWithOpts(
				client.WithHost(server.URL), client.WithScheme(scheme), client.WithVersion("1.48"),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, dockerClient.Close()) })
			if tc.closeServer {
				server.Close()
			}
			runner := initjob.NewInitJobRunner(dockerClient, swarm.NewSwarm(dockerClient),
				time.Millisecond, time.Second, &metrics.NopDeploys{})
			_, creationErr := runner.Run(t.Context(), initjob.Spec{StackName: "app", ServiceName: "api",
				Job: compose.InitJob{Name: "migrate", Image: "busybox:1.36"}})
			require.Error(t, creationErr)
			assert.ErrorContains(t, creationErr, "create init job service app_migrate-")
			if tc.retryable {
				var unavailable *swarm.DockerUnavailableError
				require.ErrorAs(t, creationErr, &unavailable)
				var temporary faults.Error
				require.ErrorAs(t, creationErr, &temporary)
				assert.True(t, temporary.Temporary())
			}

			now := time.Now().UTC()
			clock := func() time.Time { return now }
			db, lifecycle, bus := openService(t, WithClock(clock))
			desired := desiredServices("api")
			first, err := lifecycle.StartIfChanged(t.Context(), "app", "first", desired, true)
			require.NoError(t, err)
			require.NotNil(t, first)
			require.NoError(t, lifecycle.Complete(t.Context(), first.ID, Completion{
				Outcome: CompletionInitJobFailed, Diagnostic: creationErr,
			}))
			stored, err := deploymentstore.NewStore(db).Get(t.Context(), first.DeploymentID)
			require.NoError(t, err)
			assert.Equal(t, "init_failed", stored.ErrorCode)
			assert.Equal(t, tc.retryable, stored.Retryable)
			require.Len(t, stored.Attempts, 1)
			assert.Equal(t, tc.retryable, stored.Attempts[0].Retryable)

			// Recreate the lifecycle using only SQLite retry metadata.
			lifecycle = NewService(db, bus, WithClock(clock))
			now = now.Add(automaticRetryDelay - time.Nanosecond)
			next, err := lifecycle.StartIfChanged(t.Context(), "app", "tick", desired, false)
			require.NoError(t, err)
			assert.Nil(t, next)
			now = now.Add(time.Nanosecond)
			next, err = lifecycle.StartIfChanged(t.Context(), "app", "tick", desired, false)
			require.NoError(t, err)
			if tc.retryable {
				require.NotNil(t, next)
				assert.Equal(t, first.DeploymentID, next.DeploymentID)
				assert.Equal(t, 2, next.Number)
			} else {
				assert.Nil(t, next)
				manual, manualErr := lifecycle.StartIfChanged(t.Context(), "app", "manual", desired, false,
					StartOptions{ManualRetry: true})
				require.NoError(t, manualErr)
				require.NotNil(t, manual)
				assert.Equal(t, 2, manual.Number)
			}
		})
	}
}
