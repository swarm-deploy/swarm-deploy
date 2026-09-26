package webserver

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/swarm-deploy/swarm-deploy/internal/assistant"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/authenticator"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/handlers"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/middlewares"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops"
	recommendationstore "github.com/swarm-deploy/swarm-deploy/internal/modules/recommendations/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/ui"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	server *http.Server
}

const indexHtml = "index.html"

func buildSPAFallbackHandler(uiFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(uiFS))
	indexBytes, indexErr := fs.ReadFile(uiFS, indexHtml)
	if indexErr != nil {
		panic(fmt.Errorf("read embedded index.html: %w", indexErr))
	}

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

func NewApplication(
	address string,
	stackProvider config.StackProvider,
	gitopsModule *gitops.Module,
	swarmService *swarm.Swarm,
	eventModule *event.Module,
	resourcesModule *resources.Module,
	recommendations recommendationstore.Store,
	alerts alertstore.Store,
	assistantService assistant.Assistant,
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
		recommendations,
		alerts,
		assistantService,
	)

	apiHandler, err := generated.NewServer(h, h, generated.WithErrorHandler(handlers.HandleHTTPError))
	if err != nil {
		return nil, fmt.Errorf("build ogen api server: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)

	uiHandler := buildSPAFallbackHandler(ui.FS)
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/overview", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/overview", http.StatusMovedPermanently)
	})
	mux.Handle("/", uiHandler)

	handler := http.Handler(mux)
	auth, err := authenticator.Create(authCfg)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	handler = middlewares.NewLog(
		middlewares.Authorize(handler, auth, eventModule.Dispatcher),
		apiHandler.FindRoute,
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
