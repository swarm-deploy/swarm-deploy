package webserver

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/authenticator"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/handlers"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/middlewares"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/texts"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/logparser"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/ui"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	server *http.Server
}

const indexHtml = "index.html"

func buildSPAFallbackHandler(uiFS fs.FS, basePath, locale string) http.Handler {
	fileServer := http.FileServer(http.FS(uiFS))
	indexBytes, indexErr := fs.ReadFile(uiFS, indexHtml)
	if indexErr != nil {
		panic(fmt.Errorf("read embedded index.html: %w", indexErr))
	}
	indexBytes = injectRuntimeConfig(indexBytes, basePath, locale)

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(indexBytes))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		cleanPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleanPath == "." || cleanPath == "/" || cleanPath == "" {
			cleanPath = indexHtml
		}

		if cleanPath == indexHtml {
			serveIndex(w, r)
			return
		}

		file, err := uiFS.Open(cleanPath)
		if err != nil {
			serveIndex(w, r)
			return
		}
		defer file.Close()

		info, err := file.Stat()
		if err != nil || info.IsDir() {
			serveIndex(w, r)
			return
		}

		// Go relies on the system MIME database for .webmanifest.
		if cleanPath == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func injectRuntimeConfig(indexBytes []byte, basePath, locale string) []byte {
	baseHref := basePath
	if baseHref != "/" {
		baseHref += "/"
	}

	runtimeConfig, err := json.Marshal(struct {
		BasePath string `json:"basePath"`
		Locale   string `json:"locale"`
	}{BasePath: basePath, Locale: locale}, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		panic(fmt.Errorf("encode UI runtime config: %w", err))
	}

	injection := fmt.Sprintf(
		`<base href="%s"><script id="swarm-deploy-runtime-config" type="application/json">%s</script>`,
		html.EscapeString(baseHref),
		runtimeConfig,
	)

	indexBytes = bytes.Replace(indexBytes, []byte(`<html lang="en">`), []byte(`<html lang="`+locale+`">`), 1)
	return bytes.Replace(indexBytes, []byte("<head>"), []byte("<head>"+injection), 1)
}

func buildRouter(basePath, locale string, apiHandler http.Handler, uiFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	mountedAPIHandler := apiHandler
	mountedUIHandler := buildSPAFallbackHandler(uiFS, basePath, locale)
	if basePath != "/" {
		mountedAPIHandler = http.StripPrefix(basePath, mountedAPIHandler)
		mountedUIHandler = http.StripPrefix(basePath, mountedUIHandler)
	}

	mux.Handle(joinBasePath(basePath, "/api/"), mountedAPIHandler)
	mux.HandleFunc(joinBasePath(basePath, "/ui"), func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, joinBasePath(basePath, "/overview"), http.StatusMovedPermanently)
	})
	mux.HandleFunc(joinBasePath(basePath, "/ui/"), func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, joinBasePath(basePath, "/overview"), http.StatusMovedPermanently)
	})
	mux.Handle(basePathWithTrailingSlash(basePath), mountedUIHandler)

	return mux
}

func joinBasePath(basePath, route string) string {
	if basePath == "/" {
		return route
	}

	return basePath + route
}

func basePathWithTrailingSlash(basePath string) string {
	if basePath == "/" {
		return basePath
	}

	return basePath + "/"
}

func routeFinderWithBasePath(basePath string, findRoute middlewares.RouteFinder) middlewares.RouteFinder {
	if basePath == "/" {
		return findRoute
	}

	return func(method, requestPath string) (generated.Route, bool) {
		if requestPath != basePath && !strings.HasPrefix(requestPath, basePath+"/") {
			return generated.Route{}, false
		}

		requestPath = strings.TrimPrefix(requestPath, basePath)
		if requestPath == "" {
			requestPath = "/"
		}

		return findRoute(method, requestPath)
	}
}

func NewApplication(
	webConfig config.WebSpec,
	language config.Language,
	stackProvider config.StackProvider,
	gitopsModule *gitops.Module,
	deploymentsModule *deployments.Module,
	swarmService *swarm.Swarm,
	eventModule *event.Module,
	resourcesModule *resources.Module,
	recommendations recommendationstore.Store,
	alerts alertstore.Store,
	assistantService assistant.Assistant,
	assistantTexts *texts.Localizer,
	taskLogs *logparser.Service,
) (*Application, error) {
	h := handlers.New(
		webConfig.UI,
		stackProvider,
		gitopsModule.Store,
		gitopsModule.SyncPauses,
		gitopsModule.Controller,
		gitopsModule.GitRepository,
		swarmService,
		eventModule.History,
		resourcesModule.ServiceStore,
		resourcesModule.NodeStore,
		resourcesModule.Secrets.Store,
		resourcesModule.SecretManagers,
		recommendations,
		alerts,
		assistantService,
		assistantTexts,
		deploymentsModule.Store,
		taskLogs,
		eventModule.Dispatcher,
	)

	apiHandler, err := generated.NewServer(h, h, generated.WithErrorHandler(handlers.HandleHTTPError))
	if err != nil {
		return nil, fmt.Errorf("build ogen api server: %w", err)
	}

	handler := buildRouter(webConfig.BasePath, string(language), apiHandler, ui.FS)
	auth, err := authenticator.Create(webConfig.Security.Authentication)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	handler = middlewares.NewLog(
		middlewares.Authorize(handler, auth, eventModule.Dispatcher, webConfig.BasePath),
		routeFinderWithBasePath(webConfig.BasePath, apiHandler.FindRoute),
	)

	if tracing.Enabled() {
		handler = middlewares.Trace(handler)
	}

	handler = middlewares.Recovery(handler)

	return &Application{
		server: &http.Server{
			Addr:              webConfig.Address,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}, nil
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("web-server", a.server)
}
