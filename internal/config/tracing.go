package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/artarts36/specw"
)

const (
	TracingTransportHTTP = "http"
	TracingTransportGRPC = "grpc"

	TracingSamplerAlwaysOn                = "always_on"
	TracingSamplerAlwaysOff               = "always_off"
	TracingSamplerTraceIDRatio            = "traceidratio"
	TracingSamplerParentBasedAlwaysOn     = "parentbased_always_on"
	TracingSamplerParentBasedAlwaysOff    = "parentbased_always_off"
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

// UsesRatio reports whether Ratio controls sampling for this strategy.
func (s TracingSamplerSpec) UsesRatio() bool {
	return s.Strategy.Value == TracingSamplerTraceIDRatio || s.Strategy.Value == TracingSamplerParentBasedTraceIDRatio
}

func (c *Config) applyTracingSamplerDefaults() {
	if c.Spec.System.Tracing == nil {
		return
	}
	sampler := &c.Spec.System.Tracing.Sampler
	sampler.Strategy.Value = strings.TrimSpace(sampler.Strategy.Value)
	if sampler.Strategy.Value != "" {
		return
	}

	// Preserve the defaults of the OTel SDK for configurations that delegated sampling to it.
	strategy := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER")))
	switch strategy {
	case TracingSamplerAlwaysOn, TracingSamplerAlwaysOff, TracingSamplerParentBasedAlwaysOn,
		TracingSamplerParentBasedAlwaysOff, TracingSamplerTraceIDRatio, TracingSamplerParentBasedTraceIDRatio:
		sampler.Strategy.Value = strategy
	default:
		sampler.Strategy.Value = TracingSamplerParentBasedAlwaysOn
	}
	if sampler.UsesRatio() {
		ratio, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")), 64)
		if err != nil || math.IsNaN(ratio) || ratio < 0 || ratio > 1 {
			ratio = 1
		}
		sampler.Ratio.Value = ratio
	}
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
	if c.Spec.System.Tracing == nil {
		return nil
	}

	var errs []error

	switch c.Spec.System.Tracing.Transport {
	case TracingTransportHTTP, TracingTransportGRPC:
	default:
		errs = append(
			errs,
			fmt.Errorf(
				"system.tracing.transport must be one of %q|%q",
				TracingTransportHTTP,
				TracingTransportGRPC,
			),
		)
	}

	if err := validateTracingEndpoint(c.Spec.System.Tracing.Exporter.Endpoint.Value); err != nil {
		errs = append(errs, err)
	}

	if err := validateTracingSampler(c.Spec.System.Tracing.Sampler); err != nil {
		errs = append(errs, err)
	}

	for key := range c.Spec.System.Tracing.Exporter.Headers {
		if strings.EqualFold(strings.TrimSpace(key), "authorization") {
			errs = append(errs, errors.New("system.tracing.exporter.headers must not contain authorization"))
		}
	}

	auth := c.Spec.System.Tracing.Exporter.Authentication
	if auth.Bearer.Path != "" && strings.TrimSpace(string(auth.Bearer.Content)) == "" {
		errs = append(errs, errors.New("system.tracing.exporter.authentication.bearerPath contains empty token"))
	}

	if auth.APIKey.Path != "" && strings.TrimSpace(string(auth.APIKey.Content)) == "" {
		errs = append(errs, errors.New("system.tracing.exporter.authentication.apiKeyPath contains empty token"))
	}

	if auth.Bearer.Path != "" && auth.APIKey.Path != "" {
		errs = append(errs, errors.New(
			"system.tracing.exporter.authentication.bearerPath and apiKeyPath cannot be used together",
		))
	}

	if auth.XAPIKey.Path != "" && strings.TrimSpace(string(auth.XAPIKey.Content)) == "" {
		errs = append(errs, errors.New("system.tracing.exporter.authentication.xApiKeyPath contains empty token"))
	}

	return errs
}

func validateTracingSampler(sampler TracingSamplerSpec) error {
	strategy := strings.TrimSpace(sampler.Strategy.Value)
	if strategy == "" {
		return nil
	}

	switch strategy {
	case TracingSamplerAlwaysOn, TracingSamplerAlwaysOff,
		TracingSamplerParentBasedAlwaysOn, TracingSamplerParentBasedAlwaysOff:
		return nil
	case TracingSamplerTraceIDRatio, TracingSamplerParentBasedTraceIDRatio:
		if math.IsNaN(sampler.Ratio.Value) || sampler.Ratio.Value < 0 || sampler.Ratio.Value > 1 {
			return errors.New("system.tracing.sampler.ratio must be between 0 and 1")
		}

		return nil
	default:
		return fmt.Errorf(
			"system.tracing.sampler.strategy must be one of %q|%q|%q|%q|%q|%q",
			TracingSamplerAlwaysOn,
			TracingSamplerAlwaysOff,
			TracingSamplerTraceIDRatio,
			TracingSamplerParentBasedAlwaysOn,
			TracingSamplerParentBasedAlwaysOff,
			TracingSamplerParentBasedTraceIDRatio,
		)
	}
}

func validateTracingEndpoint(endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("system.tracing.exporter.endpoint is required")
	}

	if !strings.Contains(endpoint, "://") {
		return errors.New("system.tracing.exporter.endpoint must include http:// or https:// scheme")
	}

	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("system.tracing.exporter.endpoint is invalid: %w", err)
	}

	if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return errors.New("system.tracing.exporter.endpoint scheme must be http or https")
	}

	if parsedEndpoint.Host == "" {
		return errors.New("system.tracing.exporter.endpoint must contain host")
	}

	return nil
}
