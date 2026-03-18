package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/artarts36/swarm-deploy/internal/config"
	"github.com/artarts36/swarm-deploy/internal/controller"
	"github.com/artarts36/swarm-deploy/ui"
)

type Server struct {
	cfg     *config.Config
	control *controller.Controller
	mux     *http.ServeMux
}

func NewServer(cfg *config.Config, control *controller.Controller) *Server {
	s := &Server{
		cfg:     cfg,
		control: control,
		mux:     http.NewServeMux(),
	}

	s.registerRoutes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/v1/stacks", s.handleListStacks)
	s.mux.HandleFunc("/api/v1/sync", s.handleManualSync)

	if s.cfg.Spec.Sync.Webhook.Enabled {
		s.mux.HandleFunc(s.cfg.Spec.Sync.Webhook.Path, s.handleGitWebhook)
	}

	uiSub, err := fsSub(ui.FS, "ui")
	if err == nil {
		s.mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(uiSub))))
		s.mux.Handle("/", http.FileServer(http.FS(uiSub)))
	}
}

func (s *Server) handleListStacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stacks": s.control.ListStacks(),
		"sync":   s.control.LastSyncInfo(),
	})
}

func (s *Server) handleManualSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	queued := s.control.Trigger(controller.TriggerManual)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": queued,
	})
}

func (s *Server) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !s.validateWebhookSecret(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "invalid webhook secret",
		})
		return
	}

	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()

	queued := s.control.Trigger(controller.TriggerWebhook)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": queued,
	})
}

func (s *Server) validateWebhookSecret(r *http.Request) bool {
	expected := strings.TrimSpace(s.cfg.WebhookSecret())
	if expected == "" {
		return true
	}

	headerToken := strings.TrimSpace(r.Header.Get("X-Swarm-Deploy-Secret"))
	if headerToken == "" {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			headerToken = strings.TrimSpace(authHeader[len("Bearer "):])
		}
	}

	return headerToken == expected
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
