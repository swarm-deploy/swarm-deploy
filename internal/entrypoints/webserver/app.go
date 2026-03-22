package webserver

import (
	"fmt"
	"net/http"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/authenticator"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/handlers"
	"github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/middlewares"
	"github.com/artarts36/swarm-deploy/internal/event/dispatcher"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
	"github.com/artarts36/swarm-deploy/ui"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	server *http.Server
}

func NewApplication(
	address string,
	control *controller.Controller,
	inspector *swarm.Inspector,
	eventHistory *history.Store,
	serviceStore *service.Store,
	eventDispatcher dispatcher.Dispatcher,
	authCfg config.AuthenticationSpec,
) (*Application, error) {
	h := handlers.New(control, inspector, eventHistory, serviceStore)

	apiHandler, err := generated.NewServer(h, generated.WithErrorHandler(handlers.HandleHTTPError))
	if err != nil {
		return nil, fmt.Errorf("build ogen api server: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)

	uiHandler := http.FileServer(http.FS(ui.FS))
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	mux.Handle("/ui/", http.StripPrefix("/ui/", uiHandler))
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
