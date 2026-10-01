package webhookserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"golang.org/x/time/rate"
)

func TestValidateGitHubSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "webhook-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	_, err := mac.Write(body)
	require.NoError(t, err)

	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	assert.True(t, validateGitHubSignature(signature, body, secret))
	assert.False(t, validateGitHubSignature(signature, []byte(`{"ref":"refs/heads/dev"}`), secret))
	assert.False(t, validateGitHubSignature("sha1=deadbeef", body, secret))
	assert.False(t, validateGitHubSignature("sha256=not-hex", body, secret))
}

func TestAuthenticateWebhookAnyOf(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
	req.Header.Set("X-Custom-Secret", "header-secret")

	methods := []config.WebhookAuthSpec{
		{
			Type: config.WebhookAuthTypeGitHub,
			Secret: specw.File{
				Content: []byte("github-secret"),
			},
		},
		{
			Type:   config.WebhookAuthTypeHeader,
			Header: "X-Custom-Secret",
			Secret: specw.File{
				Content: []byte("header-secret"),
			},
		},
	}

	assert.True(t, authenticateWebhook(req, nil, methods))
}

func TestAuthenticateWebhookBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer bearer-secret")

	methods := []config.WebhookAuthSpec{
		{
			Type: config.WebhookAuthTypeBearer,
			Secret: specw.File{
				Content: []byte("bearer-secret"),
			},
		},
	}

	assert.True(t, authenticateWebhook(req, nil, methods))

	req.Header.Set("Authorization", "Bearer wrong")
	assert.False(t, authenticateWebhook(req, nil, methods))
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
