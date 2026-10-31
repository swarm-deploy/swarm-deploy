package webserver

import (
	"bytes"
	"encoding/json"
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

func buildSPAFallbackHandler(uiFS fs.FS, basePath string) http.Handler {
	fileServer := http.FileServer(http.FS(uiFS))
	indexBytes, indexErr := fs.ReadFile(uiFS, indexHtml)
	if indexErr != nil {
		panic(fmt.Errorf("read embedded index.html: %w", indexErr))
	}
	indexBytes = injectRuntimeConfig(indexBytes, basePath)

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

		if cleanPath != indexHtml {
			file, err := uiFS.Open(cleanPath)
			if err == nil {
				defer file.Close()

				info, statErr := file.Stat()
				if statErr == nil && !info.IsDir() {
					fileServer.ServeHTTP(w, r)
					return
				}
			}
		}

		serveIndex(w, r)
	})
}

func injectRuntimeConfig(indexBytes []byte, basePath string) []byte {
	baseHref := basePath
	if baseHref != "/" {
		baseHref += "/"
	}

	runtimeConfig, err := json.Marshal(struct {
		BasePath string `json:"basePath"`
	}{BasePath: basePath})
	if err != nil {
		panic(fmt.Errorf("encode UI runtime config: %w", err))
	}

	injection := fmt.Sprintf(
		`<base href="%s"><script id="swarm-deploy-runtime-config" type="application/json">%s</script>`,
		html.EscapeString(baseHref),
		runtimeConfig,
	)

	return bytes.Replace(indexBytes, []byte("<head>"), []byte("<head>"+injection), 1)
}

func buildRouter(basePath string, apiHandler http.Handler, uiFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	mountedAPIHandler := apiHandler
	mountedUIHandler := buildSPAFallbackHandler(uiFS, basePath)
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
	address string,
	basePath string,
	stackProvider config.StackProvider,
	gitopsModule *gitops.Module,
	swarmService *swarm.Swarm,
	eventModule *event.Module,
	resourcesModule *resources.Module,
	recommendations recommendationstore.Store,
	alerts alertstore.Store,
	assistantService assistant.Assistant,
	taskLogs *logparser.Service,
	authCfg config.AuthenticationSpec,
) (*Application, error) {
	h := handlers.New(
		stackProvider,
		gitopsModule.Store,
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
		gitopsModule.Deployments,
		taskLogs,
		eventModule.Dispatcher,
	)

	apiHandler, err := generated.NewServer(h, h, generated.WithErrorHandler(handlers.HandleHTTPError))
	if err != nil {
		return nil, fmt.Errorf("build ogen api server: %w", err)
	}

	handler := buildRouter(basePath, apiHandler, ui.FS)
	auth, err := authenticator.Create(authCfg)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	handler = middlewares.NewLog(
		middlewares.Authorize(handler, auth, eventModule.Dispatcher, basePath),
		routeFinderWithBasePath(basePath, apiHandler.FindRoute),
	)

	if tracing.Enabled() {
		handler = middlewares.Trace(handler)
	}

	handler = middlewares.Recovery(handler)

	return &Application{
		server: &http.Server{
			Addr:              address,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}, nil
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("web-server", a.server)
}
