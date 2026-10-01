package webhookserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
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
}

func NewApplication(address string, cfg *config.Config, control *controller.Controller) *Application {
	app := &Application{
		mux:     http.NewServeMux(),
		cfg:     cfg,
		control: control,
		limiter: rate.NewLimiter(
			rate.Limit(cfg.Spec.Sync.Webhook.RateLimit.RequestsPerSecond),
			cfg.Spec.Sync.Webhook.RateLimit.Burst,
		),
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

	if !authenticateWebhook(r, body, a.cfg.Spec.Sync.Webhook.Auth) {
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

func readBody(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) ([]byte, error) {
	defer r.Body.Close()

	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
}

func authenticateWebhook(r *http.Request, body []byte, methods []config.WebhookAuthSpec) bool {
	for _, method := range methods {
		if authenticateWebhookMethod(r, body, method) {
			return true
		}
	}

	return false
}

func authenticateWebhookMethod(r *http.Request, body []byte, method config.WebhookAuthSpec) bool {
	secret := strings.TrimSpace(string(method.Secret.Content))
	if secret == "" {
		return false
	}

	switch method.Type {
	case config.WebhookAuthTypeGitHub:
		return validateGitHubSignature(r.Header.Get("X-Hub-Signature-256"), body, secret)
	case config.WebhookAuthTypeHeader:
		return secretEqual(strings.TrimSpace(r.Header.Get(method.Header)), secret)
	case config.WebhookAuthTypeBearer:
		return validateBearer(r.Header.Get("Authorization"), secret)
	default:
		return false
	}
}

func validateGitHubSignature(signature string, body []byte, secret string) bool {
	algorithm, encodedSignature, ok := strings.Cut(strings.TrimSpace(signature), "=")
	if !ok || algorithm != "sha256" {
		return false
	}

	actualSignature, err := hex.DecodeString(encodedSignature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)

	return hmac.Equal(actualSignature, mac.Sum(nil))
}

func validateBearer(value, secret string) bool {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}

	return secretEqual(parts[1], secret)
}

func secretEqual(actual, expected string) bool {
	return hmac.Equal([]byte(actual), []byte(expected))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
