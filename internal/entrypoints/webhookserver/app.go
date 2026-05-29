package webhookserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/controller"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	mux     *http.ServeMux
	server  *http.Server
	cfg     *config.Config
	control *controller.Controller
}

func NewApplication(address string, cfg *config.Config, control *controller.Controller) *Application {
	app := &Application{
		mux:     http.NewServeMux(),
		cfg:     cfg,
		control: control,
	}

	app.registerRoutes()
	app.server = &http.Server{
		Addr:              address,
		Handler:           app.mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return app
}

func (a *Application) Enabled() bool {
	return a.cfg.Spec.Sync.Webhook.Enabled
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("WebhookServer", a.server)
}

func (a *Application) registerRoutes() {
	if !a.Enabled() {
		return
	}

	a.mux.HandleFunc(a.cfg.Spec.Sync.Webhook.Path, a.handleGitWebhook)
}

func (a *Application) handleGitWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if !a.validateWebhookSecret(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "invalid webhook secret",
		})
		return
	}

	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()

	queued := a.control.Webhook()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": queued,
	})
}

func (a *Application) validateWebhookSecret(r *http.Request) bool {
	expected := strings.TrimSpace(string(a.cfg.Spec.Sync.Webhook.Secret.Content))
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
