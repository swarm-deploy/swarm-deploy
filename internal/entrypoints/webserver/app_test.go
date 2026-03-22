package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIRoutes(t *testing.T) {
	app, err := NewApplication(":0", nil, nil, nil, nil, nil, config.AuthenticationSpec{})
	require.NoError(t, err, "new application")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	app.server.Handler.ServeHTTP(rec, req)
	assert.Equal(t, 200, rec.Code, "expected / status 200")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ui", nil)
	app.server.Handler.ServeHTTP(rec, req)
	assert.Equal(t, 301, rec.Code, "expected /ui status 301")
	assert.Equal(t, "/ui/", rec.Header().Get("Location"), "expected /ui redirect to /ui/")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ui/", nil)
	app.server.Handler.ServeHTTP(rec, req)
	assert.Equal(t, 200, rec.Code, "expected /ui/ status 200")
}
