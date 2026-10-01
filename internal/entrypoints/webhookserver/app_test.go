package webhookserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webhookserver/authenticator"
	"golang.org/x/time/rate"
)

func TestAuthenticateWebhookAnyOf(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
	req.Header.Set("X-Custom-Secret", "header-secret")

	app := &Application{
		authenticators: []authenticator.Authenticator{
			authenticator.NewGitHubAuthenticator([]byte("github-secret")),
			authenticator.NewHmacAuthenticator("X-Custom-Secret", []byte("header-secret")),
		},
	}

	assert.True(t, app.authenticate(&authenticator.Request{Request: req}))
}

func TestAuthenticateWebhookBearer(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		expected      bool
	}{
		{name: "valid token", authorization: "Bearer bearer-secret", expected: true},
		{name: "invalid token", authorization: "Bearer wrong", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
			req.Header.Set("Authorization", tt.authorization)
			app := &Application{
				authenticators: []authenticator.Authenticator{
					authenticator.NewBearerAuthenticator([]byte("bearer-secret")),
				},
			}

			assert.Equal(t, tt.expected, app.authenticate(&authenticator.Request{Request: req}))
		})
	}
}

func TestHandleGitWebhookRateLimit(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1), 1)
	require.True(t, limiter.Allow(), "consume initial burst token")

	app := &Application{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Webhook: config.WebhookSpec{
						MaxBodyBytes: 1024,
					},
				},
			},
		},
		limiter: limiter,
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString("payload"))
	rec := httptest.NewRecorder()

	app.handleGitWebhook(rec, req)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestHandleGitWebhookRejectsLargeBody(t *testing.T) {
	app := &Application{
		cfg: &config.Config{
			Spec: config.Spec{
				Sync: config.SyncSpec{
					Webhook: config.WebhookSpec{
						MaxBodyBytes: 4,
					},
				},
			},
		},
		limiter: rate.NewLimiter(rate.Inf, 1),
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString("payload"))
	rec := httptest.NewRecorder()

	app.handleGitWebhook(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}
