package webserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
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
	"golang.org/x/net/html"
)

func newTestApplication(t *testing.T, webConfig config.WebSpec, language config.Language) *Application {
	t.Helper()
	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	secretsStore := secretstore.NewSQLStore(testutil.OpenSQLite(t))
	serviceManager := swarm.NewMockServiceManager(ctrl)

	localizer, err := texts.New(string(language))
	require.NoError(t, err)
	app, err := NewApplication(
		webConfig,
		language,
		nil,
		&gitops.Module{GitRepository: gitRepository},
		&deployments.Module{},
		&swarm.Swarm{Services: serviceManager},
		&event.Module{},
		&resources.Module{Secrets: &resourcesecrets.Domain{Store: secretsStore}},
		nil,
		nil,
		nil,
		localizer,
		logparser.NewService(serviceManager),
	)
	require.NoError(t, err, "new application")
	return app
}

func TestGetUIConfig(t *testing.T) {
	testCases := []struct {
		name       string
		linkFields map[string]string
		wantJSON   string
	}{
		{
			name:       "configured links",
			linkFields: map[string]string{"trace-id": "http://tracer.example/{value}"},
			wantJSON:   `{"web":{"ui":{"logViewer":{"linkFields":{"trace-id":"http://tracer.example/{value}"}}}}}`,
		},
		{
			name:     "empty links",
			wantJSON: `{"web":{"ui":{"logViewer":{"linkFields":{}}}}}`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApplication(t, config.WebSpec{
				Address:  ":0",
				BasePath: "/swarm-deploy",
				UI:       config.WebUISpec{LogViewer: config.LogViewerSpec{LinkFields: tc.linkFields}},
			}, config.LanguageEnglish)
			rec := httptest.NewRecorder()
			app.server.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/swarm-deploy/api/v1/config", nil))

			require.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, tc.wantJSON, rec.Body.String())
		})
	}
}

func TestUIRoutes(t *testing.T) {
	app := newTestApplication(t, config.WebSpec{Address: ":0", BasePath: "/"}, config.LanguageEnglish)

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
			handler := buildRouter(testCase.basePath, "en", config.WebPWASpec{}, apiHandler, uiFS)

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
	handler := buildSPAFallbackHandler(fs.FS(uiFS), "/", "en", config.WebPWASpec{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/overview", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code, "non-read SPA request must not serve index")
}

func TestEmbeddedUIWithBasePath(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		basePath string
		prefix   string
		language config.Language
		locale   string
	}{
		{name: "root", basePath: "/", prefix: "", language: config.LanguageEnglish, locale: "en"},
		{name: "nested", basePath: "/infrastructure/swarm", prefix: "/infrastructure/swarm", language: config.LanguageRussian, locale: "ru"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app := newTestApplication(t, config.WebSpec{Address: ":0", BasePath: testCase.basePath}, testCase.language)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, testCase.prefix+"/services/my-stack/my-service", nil)
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			app.server.Handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
			assert.Contains(t, rec.Body.String(), `<base href="`+testCase.prefix+`/">`)
			assert.Contains(t, rec.Body.String(), `"basePath":"`+testCase.basePath+`"`)
			assert.Contains(t, rec.Body.String(), `"locale":"`+testCase.locale+`"`)
			assert.Contains(t, rec.Body.String(), `<html lang="`+testCase.locale+`">`)
		})
	}
}

func TestPWAURLsFromServicePage(t *testing.T) {
	shortcutIconPaths := []string{"icons/shortcut-assistant.png", "icons/shortcut-events.png"}
	uiFS := ui.FS
	if _, err := fs.Stat(uiFS, "assets"); errors.Is(err, fs.ErrNotExist) {
		// A clean checkout embeds a placeholder index. Mirror Vite's production base for Go-only runs.
		index, err := os.ReadFile("../../../ui/index.html")
		require.NoError(t, err)
		manifest, err := os.ReadFile("../../../ui/public/manifest.webmanifest")
		require.NoError(t, err)
		files := fstest.MapFS{
			"index.html":           {Data: []byte(strings.ReplaceAll(string(index), "%BASE_URL%", "./"))},
			"manifest.webmanifest": {Data: manifest},
		}
		for _, iconPath := range shortcutIconPaths {
			data, err := os.ReadFile("../../../ui/public/" + iconPath)
			require.NoError(t, err)
			files[iconPath] = &fstest.MapFile{Data: data}
		}
		uiFS = files
	}

	for _, tc := range []struct {
		name              string
		basePath          string
		prefix            string
		locale            string
		pwa               config.WebPWASpec
		wantName          string
		wantShortName     string
		wantShortcutNames [2]string
	}{
		{
			name:              "root with default name",
			basePath:          "/",
			locale:            "en",
			wantName:          "Swarm Deploy",
			wantShortName:     "Swarm Deploy",
			wantShortcutNames: [2]string{"Ask the assistant", "Events & Alerts"},
		},
		{
			name:              "prefix with custom name",
			basePath:          "/infra/swarm",
			prefix:            "/infra/swarm",
			locale:            "ru",
			pwa:               config.WebPWASpec{Name: "My Cluster", ShortName: "mc"},
			wantName:          "My Cluster",
			wantShortName:     "mc",
			wantShortcutNames: [2]string{"Спросить ассистента", "События и алерты"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const origin = "https://swarm.example"
			handler := buildRouter(tc.basePath, tc.locale, tc.pwa, http.NotFoundHandler(), uiFS)
			page := httptest.NewRequest(http.MethodGet, origin+tc.prefix+"/services/app/api", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, page)
			require.Equal(t, http.StatusOK, rec.Code)

			doc, err := html.Parse(rec.Body)
			require.NoError(t, err)
			head := findPWAElement(t, doc, "head", "")
			base := resolvePWAURL(t, page.URL, pwaAttr(findPWAElement(t, head, "base", ""), "href"))
			manifestLink := findPWAElement(t, head, "link", "manifest")
			manifestURL := resolvePWAURL(t, base, pwaAttr(manifestLink, "href"))
			appleLink := findPWAElement(t, head, "link", "apple-touch-icon")
			appleURL := resolvePWAURL(t, base, pwaAttr(appleLink, "href"))
			assert.Equal(t, "use-credentials", pwaAttr(manifestLink, "crossorigin"))
			assert.Equal(t, origin+tc.prefix+"/manifest.webmanifest", manifestURL.String())
			assert.Equal(t, origin+tc.prefix+"/icons/apple-touch-icon.png", appleURL.String())

			rec = httptest.NewRecorder()
			manifestRequest := httptest.NewRequest(http.MethodGet, manifestURL.String(), nil)
			manifestRequest.Header.Set("Accept-Language", "fr")
			handler.ServeHTTP(rec, manifestRequest)
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "application/manifest+json", rec.Header().Get("Content-Type"))
			var manifest struct {
				Name      string `json:"name"`
				ShortName string `json:"short_name"`
				StartURL  string `json:"start_url"`
				Scope     string `json:"scope"`
				Icons     []struct {
					Src string `json:"src"`
				} `json:"icons"`
				Shortcuts []struct {
					Name  string `json:"name"`
					URL   string `json:"url"`
					Icons []struct {
						Src     string `json:"src"`
						Sizes   string `json:"sizes"`
						Type    string `json:"type"`
						Purpose string `json:"purpose"`
					} `json:"icons"`
				} `json:"shortcuts"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &manifest))
			assert.Equal(t, tc.wantName, manifest.Name)
			assert.Equal(t, tc.wantShortName, manifest.ShortName)
			assert.Equal(t, origin+tc.prefix+"/", resolvePWAURL(t, manifestURL, manifest.StartURL).String())
			assert.Equal(t, origin+tc.prefix+"/", resolvePWAURL(t, manifestURL, manifest.Scope).String())

			require.Len(t, manifest.Shortcuts, 2)
			assert.Equal(t, tc.wantShortcutNames[0], manifest.Shortcuts[0].Name)
			assert.Equal(t, tc.wantShortcutNames[1], manifest.Shortcuts[1].Name)
			assert.Equal(t, origin+tc.prefix+"/overview?assistant=new", resolvePWAURL(t, manifestURL, manifest.Shortcuts[0].URL).String())
			assert.Equal(t, origin+tc.prefix+"/events", resolvePWAURL(t, manifestURL, manifest.Shortcuts[1].URL).String())

			for i, shortcut := range manifest.Shortcuts {
				require.Len(t, shortcut.Icons, 1)
				icon := shortcut.Icons[0]
				assert.Equal(t, "192x192", icon.Sizes)
				assert.Equal(t, "image/png", icon.Type)
				assert.Equal(t, "any", icon.Purpose)
				encoded, ok := strings.CutPrefix(icon.Src, "data:image/png;base64,")
				require.True(t, ok, "shortcut icon must be embedded as a PNG data URL")
				data, err := base64.StdEncoding.DecodeString(encoded)
				require.NoError(t, err)
				want, err := fs.ReadFile(uiFS, shortcutIconPaths[i])
				require.NoError(t, err)
				assert.Equal(t, want, data)
				image, err := png.Decode(bytes.NewReader(data))
				require.NoError(t, err)
				assert.Equal(t, 192, image.Bounds().Dx())
				assert.Equal(t, 192, image.Bounds().Dy())

				darkPixels, transparentPixels := 0, 0
				for y := image.Bounds().Min.Y; y < image.Bounds().Max.Y; y++ {
					for x := image.Bounds().Min.X; x < image.Bounds().Max.X; x++ {
						r, g, b, alpha := image.At(x, y).RGBA()
						switch {
						case alpha >= 0xe000 && max(r, g, b) <= 0x4000:
							darkPixels++
						case alpha == 0:
							transparentPixels++
						}
					}
				}
				pixels := image.Bounds().Dx() * image.Bounds().Dy()
				assert.Greater(t, transparentPixels, pixels/2, "shortcut icon needs a transparent background")
				assert.Greater(t, darkPixels, pixels/20, "shortcut glyph should be visible on a light launcher tile")
			}

			var icons []string
			for _, icon := range manifest.Icons {
				icons = append(icons, resolvePWAURL(t, manifestURL, icon.Src).String())
			}
			assert.ElementsMatch(t, []string{
				origin + tc.prefix + "/icons/icon-192.png",
				origin + tc.prefix + "/icons/icon-512.png",
				origin + tc.prefix + "/icons/icon-maskable-512.png",
			}, icons)
		})
	}
}

func findPWAElement(t *testing.T, parent *html.Node, tag, rel string) *html.Node {
	t.Helper()
	for node := range parent.Descendants() {
		if node.Type == html.ElementNode && node.Data == tag && (rel == "" || pwaAttr(node, "rel") == rel) {
			return node
		}
	}
	require.FailNowf(t, "missing HTML element", "tag=%q rel=%q", tag, rel)
	return nil
}

func pwaAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func resolvePWAURL(t *testing.T, base *url.URL, ref string) *url.URL {
	t.Helper()
	require.NotEmpty(t, ref)
	resolved, err := base.Parse(ref)
	require.NoError(t, err)
	return resolved
}
