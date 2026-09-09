package config

import (
	"errors"
	"fmt"
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
	// Endpoint is the OTLP exporter endpoint.
	Endpoint specw.Env[string] `yaml:"endpoint"`
	// Headers contains custom OTLP exporter headers. Authorization is reserved for Authentication.
	Headers map[string]string `yaml:"headers"`
	// Authentication contains exporter authentication settings.
	Authentication TracingAuthenticationSpec `yaml:"authentication"`
}

// TracingAuthenticationSpec contains OTLP exporter authentication settings.
type TracingAuthenticationSpec struct {
	// Bearer is a path to a file containing the bearer token.
	Bearer specw.File `yaml:"bearerPath"`
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

	if strings.TrimSpace(c.Spec.Tracing.Exporter.Endpoint.Value) == "" {
		errs = append(errs, errors.New("tracing.exporter.endpoint is required"))
	}

	for key := range c.Spec.Tracing.Exporter.Headers {
		if strings.EqualFold(strings.TrimSpace(key), "authorization") {
			errs = append(errs, errors.New("tracing.exporter.headers must not contain authorization"))
		}
	}

	bearer := c.Spec.Tracing.Exporter.Authentication.Bearer
	if bearer.Path != "" && strings.TrimSpace(string(bearer.Content)) == "" {
		errs = append(errs, errors.New("tracing.exporter.authentication.bearerPath contains empty token"))
	}

	return errs
}
