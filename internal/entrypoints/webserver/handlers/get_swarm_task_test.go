package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

func TestToGeneratedEnvironment(t *testing.T) {
	t.Parallel()

	entries := []string{
		"DB_PASSWORD=synthetic-secret",
		"PUBLIC_URL=https://user:synthetic-secret@example.test/path",
		"APP_ENV=production",
		"EMPTY=",
		"VALUE=left=middle=right",
		"UNICODE=Привет 🌍",
		"API_TOKEN=секрет🌍",
		"MALFORMED",
	}

	mapped := toGeneratedEnvironment(entries)
	require.Len(t, mapped, len(entries))

	assert.True(t, mapped[0].Encoded, "sensitive variable name must be detected")
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("synthetic-secret")), mapped[0].Value)
	decoded, err := base64.StdEncoding.DecodeString(mapped[0].Value)
	require.NoError(t, err)
	assert.Equal(t, "synthetic-secret", string(decoded))

	assert.True(t, mapped[1].Encoded, "sensitive variable value must be detected")
	decoded, err = base64.StdEncoding.DecodeString(mapped[1].Value)
	require.NoError(t, err)
	assert.Equal(t, "https://user:synthetic-secret@example.test/path", string(decoded))

	assert.Equal(t, generated.EnvironmentVariable{Name: "APP_ENV", Value: "production", Encoded: false}, mapped[2])
	assert.Equal(t, generated.EnvironmentVariable{Name: "EMPTY", Value: "", Encoded: false}, mapped[3])
	assert.Equal(t, generated.EnvironmentVariable{Name: "VALUE", Value: "left=middle=right", Encoded: false}, mapped[4])
	assert.Equal(t, generated.EnvironmentVariable{Name: "UNICODE", Value: "Привет 🌍", Encoded: false}, mapped[5])
	assert.True(t, mapped[6].Encoded)
	decoded, err = base64.StdEncoding.DecodeString(mapped[6].Value)
	require.NoError(t, err)
	assert.Equal(t, "секрет🌍", string(decoded))
	assert.Equal(t, generated.EnvironmentVariable{Name: "MALFORMED", Value: "", Encoded: false}, mapped[7])
	empty := toGeneratedEnvironment(nil)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)
}

func TestHandlerGetSwarmTask(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	exitCode := 0
	serviceInspector.EXPECT().
		GetTask(gomock.Any(), "task-1").
		Return(swarm.ServiceTaskDetails{
			ID:           "task-1",
			ServiceID:    "service-1",
			NodeID:       "node-1",
			Slot:         1,
			DesiredState: swarm.TaskStateRunning,
			CurrentState: swarm.TaskStateRunning,
			CreatedAt:    time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC),
			UpdatedAt:    time.Date(2026, time.October, 28, 12, 1, 0, 0, time.UTC),
			ContainerID:  "container-1",
			ExitCode:     &exitCode,
			Image:        "api:v2",
			Environment:  []string{"DB_PASSWORD=synthetic-secret", "LOG_LEVEL=debug"},
			Reservations: &swarm.TaskResources{NanoCPUs: 100, MemoryBytes: 200},
			Limits:       &swarm.TaskResources{NanoCPUs: 300, MemoryBytes: 400},
		}, nil)

	server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/swarm/tasks/task-1", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	assert.NotContains(t, recorder.Body.String(), "synthetic-secret")

	var response generated.SwarmTaskDetails
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Environment, 2)
	assert.True(t, response.Environment[0].Encoded)
	assert.False(t, response.Environment[1].Encoded)
	assert.Equal(t, "debug", response.Environment[1].Value)
	assert.Equal(t, "api:v2", response.Image)
	assert.Equal(t, int64(0), response.ExitCode.Value)
	reservations, ok := response.Reservations.Get()
	require.True(t, ok)
	assert.Equal(t, int64(100), reservations.NanoCpus)
}

func TestHandlerGetSwarmTaskNotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	h := &handler{serviceInspector: serviceInspector}
	serviceInspector.EXPECT().GetTask(gomock.Any(), "missing").Return(swarm.ServiceTaskDetails{}, swarm.ErrTaskNotFound)

	_, err := h.GetSwarmTask(context.Background(), generated.GetSwarmTaskParams{TaskID: "missing"})
	require.Error(t, err)
	var statusErr *statusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusNotFound, statusErr.code)
}
