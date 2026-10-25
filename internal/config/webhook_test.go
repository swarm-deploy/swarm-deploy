package config

import (
	"testing"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyWebhookDefaults(t *testing.T) {
	cfg := &Config{}

	cfg.applyWebhookDefaults()

	assert.Equal(t, defaultWebhookMaxBodyBytes, cfg.Spec.Sync.Webhook.MaxBodyBytes)
	assert.Equal(
		t,
		float64(defaultWebhookRateLimitRequestsPerSecond),
		cfg.Spec.Sync.Webhook.RateLimit.RequestsPerSecond,
	)
	assert.Equal(t, defaultWebhookRateLimitBurst, cfg.Spec.Sync.Webhook.RateLimit.Burst)
}

func TestValidateWebhookRequiresAuth(t *testing.T) {
	cfg := &Config{
		Spec: Spec{
			Sync: SyncSpec{
				Webhook: WebhookSpec{
					Enabled:      true,
					MaxBodyBytes: defaultWebhookMaxBodyBytes,
					RateLimit: WebhookRateLimitSpec{
						RequestsPerSecond: defaultWebhookRateLimitRequestsPerSecond,
						Burst:             defaultWebhookRateLimitBurst,
					},
				},
			},
		},
	}

	errs := cfg.validateWebhook()

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "sync.webhook.auth must contain at least one authentication method")
}

func TestValidateWebhookAcceptsConfiguredAuthMethods(t *testing.T) {
	cfg := &Config{
		Spec: Spec{
			Sync: SyncSpec{
				Webhook: WebhookSpec{
					Enabled:      true,
					MaxBodyBytes: defaultWebhookMaxBodyBytes,
					RateLimit: WebhookRateLimitSpec{
						RequestsPerSecond: defaultWebhookRateLimitRequestsPerSecond,
						Burst:             defaultWebhookRateLimitBurst,
					},
					Auth: []WebhookAuthSpec{
						{
							Type: WebhookAuthTypeGitHub,
							Secret: specw.File{
								Content: []byte("github-secret"),
							},
						},
						{
							Type:   WebhookAuthTypeHeader,
							Header: "X-Swarm-Deploy-Secret",
							Secret: specw.File{
								Content: []byte("header-secret"),
							},
						},
						{
							Type: WebhookAuthTypeBearer,
							Secret: specw.File{
								Content: []byte("bearer-secret"),
							},
						},
					},
				},
			},
		},
	}

	assert.Empty(t, cfg.validateWebhook())
}

func TestValidateWebhookRequiresHeaderName(t *testing.T) {
	cfg := &Config{
		Spec: Spec{
			Sync: SyncSpec{
				Webhook: WebhookSpec{
					Enabled:      true,
					MaxBodyBytes: defaultWebhookMaxBodyBytes,
					RateLimit: WebhookRateLimitSpec{
						RequestsPerSecond: defaultWebhookRateLimitRequestsPerSecond,
						Burst:             defaultWebhookRateLimitBurst,
					},
					Auth: []WebhookAuthSpec{
						{
							Type: WebhookAuthTypeHeader,
							Secret: specw.File{
								Content: []byte("secret"),
							},
						},
					},
				},
			},
		},
	}

	errs := cfg.validateWebhook()

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "header is required for type=header")
}
