package config

import (
	"testing"

	"github.com/artarts36/specw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracingExporterSpecResolveEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318/v1/traces")

	spec := TracingExporterSpec{Endpoint: "$OTEL_EXPORTER_OTLP_ENDPOINT"}

	assert.Equal(t, "http://otel-collector:4318/v1/traces", spec.ResolveEndpoint())
}

func TestConfigValidateTracing(t *testing.T) {
	tests := []struct {
		name    string
		tracing *TracingSpec
		wantErr string
	}{
		{
			name: "disabled",
		},
		{
			name: "http",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{Endpoint: "http://otel-collector:4318/v1/traces"},
			},
		},
		{
			name: "grpc",
			tracing: &TracingSpec{
				Transport: TracingTransportGRPC,
				Exporter: TracingExporterSpec{Endpoint: "http://otel-collector:4317"},
			},
		},
		{
			name: "unsupported transport",
			tracing: &TracingSpec{
				Transport: "udp",
				Exporter: TracingExporterSpec{Endpoint: "http://otel-collector:4318"},
			},
			wantErr: "tracing.transport must be one of",
		},
		{
			name: "empty endpoint",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
			},
			wantErr: "tracing.exporter.endpoint is required",
		},
		{
			name: "authorization header",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: "http://otel-collector:4318/v1/traces",
					Headers: map[string]string{" Authorization ": "Bearer forbidden"},
				},
			},
			wantErr: "tracing.exporter.headers must not contain authorization",
		},
		{
			name: "empty bearer token",
			tracing: &TracingSpec{
				Transport: TracingTransportHTTP,
				Exporter: TracingExporterSpec{
					Endpoint: "http://otel-collector:4318/v1/traces",
					Authentication: TracingAuthenticationSpec{
						Bearer: specw.File{Path: "/run/secrets/otel-collector"},
					},
				},
			},
			wantErr: "tracing.exporter.authentcation.bearerPath contains empty token",
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
			assert.Contains(t, errorsText(errs), tt.wantErr)
		})
	}
}

func errorsText(errs []error) string {
	var out string
	for _, err := range errs {
		out += err.Error() + "\n"
	}
	return out
}
