package healthserver

import (
	"net/http"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	mux *http.ServeMux

	server *http.Server
}

func NewApplication(cfg config.HealthServerSpec) *Application {
	s := &Application{
		mux: http.NewServeMux(),
	}

	s.mux.HandleFunc(cfg.Healthz.Path, s.handleHealth)
	s.mux.Handle(cfg.Metrics.Path, promhttp.Handler())

	s.server = &http.Server{
		Addr:              cfg.Address,
		Handler:           s.mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return s
}

func (s *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("health-server", s.server)
}

func (s *Application) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
