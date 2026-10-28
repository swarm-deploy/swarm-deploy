package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	deploymentstore "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestGetStackDeploymentActivityHTTP(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name        string
		path        string
		status      int
		bucketCount int
	}{
		{name: "weekly default", path: "/api/v1/stacks/app/deployments/activity", status: http.StatusOK, bucketCount: 8},
		{name: "daily", path: "/api/v1/stacks/app/deployments/activity?interval=daily", status: http.StatusOK, bucketCount: 30},
		{name: "invalid interval", path: "/api/v1/stacks/app/deployments/activity?interval=monthly", status: http.StatusBadRequest},
		{name: "unknown stack", path: "/api/v1/stacks/unknown/deployments/activity", status: http.StatusNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			h := &handler{
				stackProvider: newConfigWithStacks(t, []config.StackSpec{{Name: "app", ComposeFile: "app.yaml"}}),
				deployments:   deploymentstore.NewStore(testutil.OpenSQLite(t)),
			}
			server, err := generated.NewServer(h, h, generated.WithErrorHandler(HandleHTTPError))
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.path, nil))
			require.Equal(t, testCase.status, recorder.Code, recorder.Body.String())
			if testCase.status != http.StatusOK {
				return
			}
			var response generated.DeploymentActivityResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Len(t, response.Buckets, testCase.bucketCount)
			for i := 1; i < len(response.Buckets); i++ {
				assert.True(t, response.Buckets[i-1].Start.Before(response.Buckets[i].Start))
			}
		})
	}
}

func TestDeploymentActivityBoundsUseUTCAndMondayWeeks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 28, 23, 30, 0, 0, time.FixedZone("west", -7*60*60))
	for _, testCase := range []struct {
		name     string
		interval deploymentstore.ActivityInterval
		start    time.Time
		end      time.Time
	}{
		{
			name: "daily", interval: deploymentstore.ActivityIntervalDaily,
			start: time.Date(2026, time.September, 30, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2026, time.October, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "weekly", interval: deploymentstore.ActivityIntervalWeekly,
			start: time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC),
			end:   time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			start, end := deploymentActivityBounds(testCase.interval, now)
			assert.Equal(t, testCase.start, start)
			assert.Equal(t, testCase.end, end)
		})
	}
}
