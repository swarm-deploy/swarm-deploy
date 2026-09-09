package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/artarts36/specw"
)

const (
	TracingTransportHTTP = "http"
	TracingTransportGRPC = "grpc"
)

// TracingSpec contains OpenTelemetry tracing settings.
type TracingSpec struct {
	// Transport is the OTLP transport: http or grpc.
	Transport string `yaml:"transport"`
	// Exporter contains OTLP exporter settings.
	Exporter TracingExporterSpec `yaml:"exporter"`
}

// TracingExporterSpec contains OTLP exporter connection settings.
type TracingExporterSpec struct {
	// Endpoint is the OTLP exporter endpoint. Environment variables are expanded before use.
	Endpoint string `yaml:"endpoint"`
	// Headers contains custom OTLP exporter headers. Authorization is reserved for Authentication.
	Headers map[string]string `yaml:"headers"`
	// Authentication contains exporter authentication settings.
	Authentication TracingAuthenticationSpec `yaml:"authentcation"`
}

// TracingAuthenticationSpec contains OTLP exporter authentication settings.
type TracingAuthenticationSpec struct {
	// Bearer is a path to a file containing the bearer token.
	Bearer specw.File `yaml:"bearerPath"`
}

// ResolveEndpoint expands environment variables in the configured exporter endpoint.
func (e TracingExporterSpec) ResolveEndpoint() string {
	return strings.TrimSpace(os.ExpandEnv(strings.TrimSpace(e.Endpoint)))
}

func (c *Config) validateTracing() []error {
	if c.Spec.Tracing == nil {
		return nil
	}

	var errs []error

	switch c.Spec.Tracing.Transport {
	case TracingTransportHTTP, TracingTransportGRPC:
	default:
		errs = append(
			errs,
			fmt.Errorf(
				"tracing.transport must be one of %q|%q",
				TracingTransportHTTP,
				TracingTransportGRPC,
			),
		)
	}

	if c.Spec.Tracing.Exporter.ResolveEndpoint() == "" {
		errs = append(errs, errors.New("tracing.exporter.endpoint is required"))
	}

	for key := range c.Spec.Tracing.Exporter.Headers {
		if strings.EqualFold(strings.TrimSpace(key), "authorization") {
			errs = append(errs, errors.New("tracing.exporter.headers must not contain authorization"))
		}
	}

	bearer := c.Spec.Tracing.Exporter.Authentication.Bearer
	if bearer.Path != "" && strings.TrimSpace(string(bearer.Content)) == "" {
		errs = append(errs, errors.New("tracing.exporter.authentcation.bearerPath contains empty token"))
	}

	return errs
}
