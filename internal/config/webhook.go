package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/artarts36/specw"
)

const (
	WebhookAuthTypeGitHub WebhookAuthType = "github"
	WebhookAuthTypeHeader WebhookAuthType = "header"
	WebhookAuthTypeBearer WebhookAuthType = "bearer"

	defaultWebhookRateLimitRequestsPerSecond = 5
	defaultWebhookRateLimitBurst             = 10
)

type WebhookAuthType string

func (t WebhookAuthType) IsSupported() bool {
	switch t {
	case WebhookAuthTypeGitHub, WebhookAuthTypeHeader, WebhookAuthTypeBearer:
		return true
	default:
		return false
	}
}

type WebhookAuthSpec struct {
	// Type selects the authentication mechanism: github, header, or bearer.
	Type WebhookAuthType `yaml:"type"`
	// Secret is a path to the secret used by the selected authentication mechanism.
	Secret specw.File `yaml:"secretPath"`
	// Header is the request header containing the secret when type=header.
	Header string `yaml:"header,omitempty"`
}

type WebhookRateLimitSpec struct {
	// RequestsPerSecond is the global request rate accepted by the webhook endpoint.
	RequestsPerSecond float64 `yaml:"requestsPerSecond"`
	// Burst is the maximum burst accepted by the webhook endpoint.
	Burst int `yaml:"burst"`
}

func (c *Config) applyWebhookDefaults() {
	if c.Spec.Sync.Webhook.RateLimit.RequestsPerSecond == 0 {
		c.Spec.Sync.Webhook.RateLimit.RequestsPerSecond = defaultWebhookRateLimitRequestsPerSecond
	}
	if c.Spec.Sync.Webhook.RateLimit.Burst == 0 {
		c.Spec.Sync.Webhook.RateLimit.Burst = defaultWebhookRateLimitBurst
	}
}

func (c *Config) validateWebhook() []error {
	if !c.Spec.Sync.Webhook.Enabled {
		return nil
	}

	var errs []error

	if len(c.Spec.Sync.Webhook.Auth) == 0 {
		errs = append(errs, errors.New("sync.webhook.auth must contain at least one authentication method"))
	}

	for i, auth := range c.Spec.Sync.Webhook.Auth {
		if !auth.Type.IsSupported() {
			errs = append(
				errs,
				fmt.Errorf("sync.webhook.auth[%d].type must be one of github|header|bearer, got %q", i, auth.Type),
			)
			continue
		}

		if strings.TrimSpace(string(auth.Secret.Content)) == "" {
			errs = append(errs, fmt.Errorf("sync.webhook.auth[%d].secretPath contains empty secret", i))
		}

		if auth.Type == WebhookAuthTypeHeader && strings.TrimSpace(auth.Header) == "" {
			errs = append(errs, fmt.Errorf("sync.webhook.auth[%d].header is required for type=header", i))
		}
	}

	if c.Spec.Sync.Webhook.RateLimit.RequestsPerSecond <= 0 {
		errs = append(errs, errors.New("sync.webhook.rateLimit.requestsPerSecond must be > 0"))
	}
	if c.Spec.Sync.Webhook.RateLimit.Burst <= 0 {
		errs = append(errs, errors.New("sync.webhook.rateLimit.burst must be > 0"))
	}

	return errs
}
