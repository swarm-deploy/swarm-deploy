package web

import (
	"net/http"

	"github.com/artarts36/swarm-deploy/internal/config"
)

type HealthServer struct {
	mux *http.ServeMux
}

func NewHealthServer(cfg config.HealthServerSpec, metricsHandler http.Handler) *HealthServer {
	s := &HealthServer{
		mux: http.NewServeMux(),
	}

	if cfg.Healthz.EnabledOrDefault(true) && cfg.Healthz.Path != "" {
		s.mux.HandleFunc(cfg.Healthz.Path, s.handleHealth)
	}
	if cfg.Metrics.EnabledOrDefault(false) && metricsHandler != nil && cfg.Metrics.Path != "" {
		s.mux.Handle(cfg.Metrics.Path, metricsHandler)
	}

	return s
}

func (s *HealthServer) Handler() http.Handler {
	return s.mux
}

func (s *HealthServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
