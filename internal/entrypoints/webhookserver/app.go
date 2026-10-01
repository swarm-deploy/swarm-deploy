package webhookserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver/authenticator"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"
	"golang.org/x/time/rate"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	mux     *http.ServeMux
	server  *http.Server
	cfg     *config.Config
	control *controller.Controller
	limiter *rate.Limiter

	authenticator authenticator.Authenticator
}

func NewApplication(address string, cfg *config.Config, control *controller.Controller) *Application {
	authenticators := make([]authenticator.Authenticator, 0, len(cfg.Spec.Sync.Webhook.Auth))

	app := &Application{
		mux:     http.NewServeMux(),
		cfg:     cfg,
		control: control,
		limiter: rate.NewLimiter(
			rate.Limit(cfg.Spec.Sync.Webhook.RateLimit.RequestsPerSecond),
			cfg.Spec.Sync.Webhook.RateLimit.Burst,
		),
	}

	for _, method := range cfg.Spec.Sync.Webhook.Auth {
		switch method.Type {
		case config.WebhookAuthTypeBearer:
			authenticators = append(authenticators, authenticator.NewBearerAuthenticator(method.Secret.Content))
		case config.WebhookAuthTypeGitHub:
			authenticators = append(authenticators, authenticator.NewGitHubAuthenticator(method.Secret.Content))
		case config.WebhookAuthTypeHeader:
			authenticators = append(authenticators, authenticator.NewHmacAuthenticator(
				method.Header,
				method.Secret.Content,
			))
		}
	}
	app.authenticator = authenticator.NewComposeAuthenticator(authenticators...)

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

	if !a.limiter.Allow() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "webhook rate limit exceeded",
		})
		return
	}

	body, err := readBody(w, r, a.cfg.Spec.Sync.Webhook.MaxBodyBytes)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error": "webhook body is too large",
			})
			return
		}

		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "failed to read webhook body",
		})
		return
	}

	if !a.authenticate(&authenticator.Request{
		Request: r,
		Body:    body,
	}) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "webhook authentication failed",
		})
		return
	}

	queued := a.control.Webhook(r.Context())
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": queued,
	})
}

func (a *Application) authenticate(req *authenticator.Request) bool {
	return a.authenticator.Authenticate(req) == nil
}

func readBody(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) ([]byte, error) {
	defer r.Body.Close()

	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
