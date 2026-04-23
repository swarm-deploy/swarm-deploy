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
	"github.com/swarm-deploy/swarm-deploy/internal/controller"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/authenticator"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/handlers"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/middlewares"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/history"
	swarmnode "github.com/swarm-deploy/swarm-deploy/internal/node"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/swarm-deploy/ui"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	server *http.Server
}

func buildSPAFallbackHandler(uiFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(uiFS))
	indexBytes, indexErr := fs.ReadFile(uiFS, "index.html")
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
			cleanPath = "index.html"
		}

		if cleanPath == "index.html" {
			serveIndex(w, r)
			return
		}

		if cleanPath != "index.html" {
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
	control *controller.Controller,
	serviceInspector *swarm.ServiceManager,
	eventHistory *history.Store,
	serviceStore *service.Store,
	nodeStore *swarmnode.Store,
	assistantService assistant.Assistant,
	eventDispatcher dispatcher.Dispatcher,
	authCfg config.AuthenticationSpec,
) (*Application, error) {
	h := handlers.New(control, serviceInspector, eventHistory, serviceStore, nodeStore, assistantService)

	apiHandler, err := generated.NewServer(h, generated.WithErrorHandler(handlers.HandleHTTPError))
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

	rootHandler := http.Handler(mux)
	auth, err := authenticator.Create(authCfg)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	return &Application{
		server: &http.Server{
			Addr: address,
			Handler: middlewares.NewLog(
				middlewares.Authorize(rootHandler, auth, eventDispatcher),
				apiHandler.FindRoute,
			),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}, nil
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("web-server", a.server)
}
