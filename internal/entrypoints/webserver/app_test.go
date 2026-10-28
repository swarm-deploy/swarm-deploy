package webserver

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	resourcesecrets "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets"
	secretstore "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
	"github.com/swarm-deploy/swarm-deploy/ui"
	"go.uber.org/mock/gomock"
)

func TestUIRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	secretsStore := secretstore.NewSQLStore(testutil.OpenSQLite(t))
	serviceManager := swarm.NewMockServiceManager(ctrl)

	app, err := NewApplication(
		":0",
		"/",
		nil,
		&gitops.Module{GitRepository: gitRepository},
		&swarm.Swarm{Services: serviceManager},
		&event.Module{},
		&resources.Module{Secrets: &resourcesecrets.Domain{Store: secretsStore}},
		nil,
		nil,
		nil,
		logparser.NewService(serviceManager),
		config.AuthenticationSpec{},
	)
	require.NoError(t, err, "new application")

	testCases := []struct {
		name           string
		path           string
		wantCode       int
		wantLocation   string
		locationAssert bool
	}{
		{
			name:     "root serves spa index",
			path:     "/",
			wantCode: 200,
		},
		{
			name:     "overview route uses spa fallback",
			path:     "/overview",
			wantCode: 200,
		},
		{
			name:     "secrets route uses spa fallback",
			path:     "/secrets",
			wantCode: 200,
		},
		{
			name:           "ui root redirects to overview",
			path:           "/ui",
			wantCode:       301,
			wantLocation:   "/overview",
			locationAssert: true,
		},
		{
			name:           "ui prefix redirects to overview",
			path:           "/ui/legacy",
			wantCode:       301,
			wantLocation:   "/overview",
			locationAssert: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			app.server.Handler.ServeHTTP(rec, req)

			assert.Equal(t, testCase.wantCode, rec.Code, "status mismatch")
			if testCase.locationAssert {
				assert.Equal(t, testCase.wantLocation, rec.Header().Get("Location"), "redirect mismatch")
			}
		})
	}
}

func TestBuildRouterSupportsBasePath(t *testing.T) {
	testCases := []struct {
		name               string
		basePath           string
		requestPath        string
		wantCode           int
		wantLocation       string
		wantBody           string
		wantAPIRequestPath string
	}{
		{
			name:        "root deep link",
			basePath:    "/",
			requestPath: "/services/app/api",
			wantCode:    http.StatusOK,
			wantBody:    `"basePath":"/"`,
		},
		{
			name:        "nested deep link",
			basePath:    "/infrastructure/swarm",
			requestPath: "/infrastructure/swarm/services/app/api",
			wantCode:    http.StatusOK,
			wantBody:    `<base href="/infrastructure/swarm/">`,
		},
		{
			name:        "nested static asset",
			basePath:    "/infrastructure/swarm",
			requestPath: "/infrastructure/swarm/assets/app.js",
			wantCode:    http.StatusOK,
			wantBody:    "console.log('app')",
		},
		{
			name:               "nested API",
			basePath:           "/infrastructure/swarm",
			requestPath:        "/infrastructure/swarm/api/v1/stacks",
			wantCode:           http.StatusTeapot,
			wantAPIRequestPath: "/api/v1/stacks",
		},
		{
			name:               "nested SSE",
			basePath:           "/infrastructure/swarm",
			requestPath:        "/infrastructure/swarm/api/tasks/task-1/logs",
			wantCode:           http.StatusTeapot,
			wantAPIRequestPath: "/api/tasks/task-1/logs",
		},
		{
			name:         "nested UI redirect",
			basePath:     "/infrastructure/swarm",
			requestPath:  "/infrastructure/swarm/ui/legacy",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "/infrastructure/swarm/overview",
		},
		{
			name:        "API outside nested base path is not SPA fallback",
			basePath:    "/infrastructure/swarm",
			requestPath: "/api/v1/stacks",
			wantCode:    http.StatusNotFound,
			wantBody:    "404 page not found",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var apiRequestPath string
			apiHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				apiRequestPath = r.URL.Path
				w.WriteHeader(http.StatusTeapot)
			})
			uiFS := fstest.MapFS{
				"index.html":    &fstest.MapFile{Data: []byte("<html><head></head><body>app</body></html>")},
				"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
			}
			handler := buildRouter(testCase.basePath, apiHandler, uiFS)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, testCase.requestPath, nil)
			handler.ServeHTTP(rec, req)

			assert.Equal(t, testCase.wantCode, rec.Code, "status mismatch")
			assert.Equal(t, testCase.wantLocation, rec.Header().Get("Location"), "redirect mismatch")
			if testCase.wantBody != "" {
				assert.Contains(t, rec.Body.String(), testCase.wantBody, "body mismatch")
			}
			assert.Equal(t, testCase.wantAPIRequestPath, apiRequestPath, "API path mismatch")
		})
	}
}

func TestBuildSPAFallbackRejectsNonReadRequests(t *testing.T) {
	uiFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>app</body></html>")},
	}
	handler := buildSPAFallbackHandler(fs.FS(uiFS), "/")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/overview", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code, "non-read SPA request must not serve index")
}

func TestEmbeddedUIWithBasePath(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		basePath string
		prefix   string
	}{
		{name: "root", basePath: "/", prefix: ""},
		{name: "nested", basePath: "/infrastructure/swarm", prefix: "/infrastructure/swarm"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var gotAPIPath string
			api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAPIPath = r.URL.Path
				if r.URL.Path != "/api/v1/stacks" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			handler := buildRouter(testCase.basePath, api, ui.FS)
			request := func(requestPath string) *httptest.ResponseRecorder {
				t.Helper()
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, requestPath, nil))
				return rec
			}

			index := request(testCase.prefix + "/services/my-stack/my-service")
			require.Equal(t, http.StatusOK, index.Code)
			require.Contains(t, index.Header().Get("Content-Type"), "text/html")
			baseHref := testCase.prefix + "/"
			assert.Contains(t, index.Body.String(), `<base href="`+baseHref+`">`)
			assert.Contains(t, index.Body.String(), `"basePath":"`+testCase.basePath+`"`)

			// The real Vite manifest in the embedded index must load both JS
			// and CSS from the same URL prefix (not from the domain root).
			assets := regexp.MustCompile(`(?:src|href)="\./(assets/[^"]+\.(?:js|css))"`).
				FindAllStringSubmatch(index.Body.String(), -1)
			require.NotEmpty(t, assets, "embedded index should reference generated assets")
			seen := make(map[string]bool)
			for _, asset := range assets {
				assetPath := asset[1]
				response := request(testCase.prefix + "/" + assetPath)
				require.Equal(t, http.StatusOK, response.Code, assetPath)
				assert.NotEmpty(t, response.Body.Bytes(), assetPath)
				if strings.HasSuffix(assetPath, ".js") {
					seen["js"] = true
					assert.Contains(t, response.Header().Get("Content-Type"), "javascript", assetPath)
				}
				if strings.HasSuffix(assetPath, ".css") {
					seen["css"] = true
					assert.Contains(t, response.Header().Get("Content-Type"), "text/css", assetPath)
				}
			}
			assert.True(t, seen["js"], "embedded index must reference JavaScript")
			assert.True(t, seen["css"], "embedded index must reference CSS")

			apiResponse := request(testCase.prefix + "/api/v1/stacks")
			assert.Equal(t, http.StatusNoContent, apiResponse.Code)
			assert.Equal(t, "/api/v1/stacks", gotAPIPath)

			unknownAPI := request(testCase.prefix + "/api/v1/does-not-exist")
			assert.Equal(t, http.StatusNotFound, unknownAPI.Code)
			assert.NotContains(t, unknownAPI.Body.String(), "<html")

			redirect := request(testCase.prefix + "/ui")
			assert.Equal(t, http.StatusMovedPermanently, redirect.Code)
			assert.Equal(t, testCase.prefix+"/overview", redirect.Header().Get("Location"))

			if testCase.prefix != "" {
				outsideAPI := request("/api/v1/stacks")
				assert.Equal(t, http.StatusNotFound, outsideAPI.Code)
				assert.NotContains(t, outsideAPI.Body.String(), "<html")
			}
		})
	}
}
