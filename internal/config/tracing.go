package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/artarts36/specw"
)

const (
	TracingTransportHTTP = "http"
	TracingTransportGRPC = "grpc"

	TracingSamplerAlwaysOn                = "always_on"
	TracingSamplerAlwaysOff               = "always_off"
	TracingSamplerTraceIDRatio            = "traceidratio"
	TracingSamplerParentBasedTraceIDRatio = "parentbased_traceidratio"
)

// TracingSpec contains OpenTelemetry tracing settings.
type TracingSpec struct {
	// Transport is the OTLP transport: http or grpc.
	Transport string `yaml:"transport"`
	// Sampler contains trace sampling settings.
	Sampler TracingSamplerSpec `yaml:"sampler"`
	// Exporter contains OTLP exporter settings.
	Exporter TracingExporterSpec `yaml:"exporter"`
}

// TracingSamplerSpec contains OpenTelemetry sampling settings.
type TracingSamplerSpec struct {
	// Strategy is the sampling strategy.
	Strategy specw.Env[string] `yaml:"strategy"`
	// Ratio is the sampling ratio for traceidratio strategies.
	Ratio specw.Env[float64] `yaml:"ratio"`
}

// TracingExporterSpec contains OTLP exporter connection settings.
type TracingExporterSpec struct {
	// Endpoint is the OTLP exporter endpoint.
	Endpoint specw.Env[string] `yaml:"endpoint"`
	// Headers contains custom OTLP exporter headers. Authorization is reserved for Authentication.
	Headers map[string]specw.Env[string] `yaml:"headers"`
	// Authentication contains exporter authentication settings.
	Authentication TracingAuthenticationSpec `yaml:"authentication"`
}

// TracingAuthenticationSpec contains OTLP exporter authentication settings.
type TracingAuthenticationSpec struct {
	// Bearer is a path to a file containing the bearer token.
	Bearer specw.File `yaml:"bearerPath"`
	// APIKey is a path to a file containing the API key used in Authorization header.
	APIKey specw.File `yaml:"apiKeyPath"`
	// XAPIKey is a path to a file containing the x-api-key token.
	XAPIKey specw.File `yaml:"xApiKeyPath"`
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

	if err := validateTracingEndpoint(c.Spec.Tracing.Exporter.Endpoint.Value); err != nil {
		errs = append(errs, err)
	}

	if err := validateTracingSampler(c.Spec.Tracing.Sampler); err != nil {
		errs = append(errs, err)
	}

	for key := range c.Spec.Tracing.Exporter.Headers {
		if strings.EqualFold(strings.TrimSpace(key), "authorization") {
			errs = append(errs, errors.New("tracing.exporter.headers must not contain authorization"))
		}
	}

	auth := c.Spec.Tracing.Exporter.Authentication
	if auth.Bearer.Path != "" && strings.TrimSpace(string(auth.Bearer.Content)) == "" {
		errs = append(errs, errors.New("tracing.exporter.authentication.bearerPath contains empty token"))
	}

	if auth.APIKey.Path != "" && strings.TrimSpace(string(auth.APIKey.Content)) == "" {
		errs = append(errs, errors.New("tracing.exporter.authentication.apiKeyPath contains empty token"))
	}

	if auth.Bearer.Path != "" && auth.APIKey.Path != "" {
		errs = append(errs, errors.New("tracing.exporter.authentication.bearerPath and apiKeyPath cannot be used together"))
	}

	if auth.XAPIKey.Path != "" && strings.TrimSpace(string(auth.XAPIKey.Content)) == "" {
		errs = append(errs, errors.New("tracing.exporter.authentication.xApiKeyPath contains empty token"))
	}

	return errs
}

func validateTracingSampler(sampler TracingSamplerSpec) error {
	strategy := strings.TrimSpace(sampler.Strategy.Value)
	if strategy == "" {
		return nil
	}

	switch strategy {
	case TracingSamplerAlwaysOn, TracingSamplerAlwaysOff:
		return nil
	case TracingSamplerTraceIDRatio, TracingSamplerParentBasedTraceIDRatio:
		if sampler.Ratio.Value < 0 || sampler.Ratio.Value > 1 {
			return errors.New("tracing.sampler.ratio must be between 0 and 1")
		}

		return nil
	default:
		return fmt.Errorf(
			"tracing.sampler.strategy must be one of %q|%q|%q|%q",
			TracingSamplerAlwaysOn,
			TracingSamplerAlwaysOff,
			TracingSamplerTraceIDRatio,
			TracingSamplerParentBasedTraceIDRatio,
		)
	}
}

func validateTracingEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("tracing.exporter.endpoint is required")
	}

	if !strings.Contains(endpoint, "://") {
		return errors.New("tracing.exporter.endpoint must include http:// or https:// scheme")
	}

	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("tracing.exporter.endpoint is invalid: %w", err)
	}

	if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return errors.New("tracing.exporter.endpoint scheme must be http or https")
	}

	if parsedEndpoint.Host == "" {
		return errors.New("tracing.exporter.endpoint must contain host")
	}

	return nil
}
