package config

import (
	"errors"
	"testing"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTracingExporterSpecEndpointFromEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318/v1/traces")

	var spec TracingExporterSpec
	err := yaml.Unmarshal([]byte("endpoint: $OTEL_EXPORTER_OTLP_ENDPOINT\n"), &spec)
	require.NoError(t, err)

	assert.Equal(t, "http://otel-collector:4318/v1/traces", spec.Endpoint.Value)
}

func TestTracingExporterSpecHeadersFromEnv(t *testing.T) {
	t.Setenv("OTEL_TENANT", "platform")

	var spec TracingExporterSpec
	err := yaml.Unmarshal([]byte("headers:\n  x-tenant: $OTEL_TENANT\n"), &spec)
	require.NoError(t, err)

	assert.Equal(t, "platform", spec.Headers["x-tenant"].Value)
}

func TestConfigValidateTracing(t *testing.T) {
	tests := []struct {
		name    string
		tracing *TracingSpec
		wantErr string
	}{
		{name: "disabled"},
		{
			name: "http",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"}},
			},
		},
		{
			name: "grpc",
			tracing: &TracingSpec{
				Transport: TracingTransportGRPC,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "http://otel-collector:4317"}},
			},
		},
		{
			name: "unsupported transport",
			tracing: &TracingSpec{
				Transport: "udp",
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "http://otel-collector:4318"}},
			},
			wantErr: "tracing.transport must be one of",
		},
		{
			name: "empty endpoint",
			tracing: &TracingSpec{Transport: TracingTransportHTTP},
			wantErr: "tracing.exporter.endpoint is required",
		},
		{
			name: "endpoint without scheme",
			tracing: &TracingSpec{
				Transport: TracingTransportGRPC,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "otel-collector:4317"}},
			},
			wantErr: "tracing.exporter.endpoint must include http:// or https:// scheme",
		},
		{
			name: "unsupported endpoint scheme",
			tracing: &TracingSpec{
				Transport: TracingTransportGRPC,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "grpc://otel-collector:4317"}},
			},
			wantErr: "tracing.exporter.endpoint scheme must be http or https",
		},
		{
			name: "endpoint without host",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "http:///v1/traces"}},
			},
			wantErr: "tracing.exporter.endpoint must contain host",
		},
		{
			name: "invalid endpoint",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{Endpoint: specw.Env[string]{Value: "http://[::1"}},
			},
			wantErr: "tracing.exporter.endpoint is invalid",
		},
		{
			name: "authorization header",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"},
					Headers: map[string]specw.Env[string]{" Authorization ": {Value: "Bearer forbidden"}},
				},
			},
			wantErr: "tracing.exporter.headers must not contain authorization",
		},
		{
			name: "empty bearer token",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"},
					Authentication: TracingAuthenticationSpec{Bearer: specw.File{Path: "/run/secrets/otel-bearer"}},
				},
			},
			wantErr: "tracing.exporter.authentication.bearerPath contains empty token",
		},
		{
			name: "empty api key",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"},
					Authentication: TracingAuthenticationSpec{APIKey: specw.File{Path: "/run/secrets/otel-api-key"}},
				},
			},
			wantErr: "tracing.exporter.authentication.apiKeyPath contains empty token",
		},
		{
			name: "bearer and api key",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"},
					Authentication: TracingAuthenticationSpec{
						Bearer: specw.File{Path: "/run/secrets/otel-bearer", Content: []byte("token")},
						APIKey: specw.File{Path: "/run/secrets/otel-api-key", Content: []byte("key")},
					},
				},
			},
			wantErr: "tracing.exporter.authentication.bearerPath and apiKeyPath cannot be used together",
		},
		{
			name: "empty x api key",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: specw.Env[string]{Value: "http://otel-collector:4318/v1/traces"},
					Authentication: TracingAuthenticationSpec{XAPIKey: specw.File{Path: "/run/secrets/otel-x-api-key"}},
				},
			},
			wantErr: "tracing.exporter.authentication.xApiKeyPath contains empty token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Spec: Spec{Tracing: tt.tracing}}

			errs := cfg.validateTracing()
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}

			require.NotEmpty(t, errs)
			assert.Contains(t, errors.Join(errs...).Error(), tt.wantErr)
		})
	}
}
