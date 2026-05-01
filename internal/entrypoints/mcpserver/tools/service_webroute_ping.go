package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/service"
)

const webRoutePingTimeout = 5 * time.Second

// HTTPDoer executes HTTP requests.
type HTTPDoer interface {
	// Do executes an HTTP request.
	Do(request *http.Request) (*http.Response, error)
}

// PingWebRoutes checks availability of service web routes from service metadata.
type PingWebRoutes struct {
	services ServicesReader
	client   HTTPDoer
}

type pingWebRoutesRequest struct {
	Service string `json:"service"`
	Stack   string `json:"stack"`
}

// NewPingWebRoutes creates service_webroute_ping component.
func NewPingWebRoutes(services ServicesReader) *PingWebRoutes {
	return &PingWebRoutes{
		services: services,
		client: &http.Client{
			Timeout: webRoutePingTimeout,
		},
	}
}

// Definition returns tool metadata visible to the model.
func (p *PingWebRoutes) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "service_webroute_ping",
		Description: "Checks web routes for a specific service from service.store and returns HTTP results for each route.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"service",
			},
			"properties": map[string]any{
				"service": map[string]any{
					"type":        "string",
					"description": "Service name to ping.",
				},
				"stack": map[string]any{
					"type":        "string",
					"description": "Optional stack name. Required only when service name exists in multiple stacks.",
				},
			},
		},
		Request: pingWebRoutesRequest{},
	}
}

// Execute runs service_webroute_ping tool.
func (p *PingWebRoutes) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	if p.services == nil {
		return routing.Response{}, fmt.Errorf("services store is not configured")
	}

	parsedRequest, err := convertRequestPayload[pingWebRoutesRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	serviceName, stackName, err := parsePingWebRoutesParams(parsedRequest)
	if err != nil {
		return routing.Response{}, err
	}

	serviceRow, err := findTargetService(p.services.List(), serviceName, stackName)
	if err != nil {
		return routing.Response{}, err
	}

	results := make([]webRoutePingResult, 0)
	for _, route := range serviceRow.WebRoutes {
		pingResult := p.pingRoute(ctx, route.Address)
		pingResult.Stack = serviceRow.Stack
		pingResult.Service = serviceRow.Name
		pingResult.Domain = route.Domain
		pingResult.Address = route.Address
		pingResult.Port = route.Port
		results = append(results, pingResult)
	}

	payload := struct {
		Results []webRoutePingResult `json:"results"`
	}{
		Results: results,
	}
	return routing.Response{Payload: payload}, nil
}

func parsePingWebRoutesParams(request pingWebRoutesRequest) (string, string, error) {
	serviceName := strings.TrimSpace(request.Service)
	if serviceName == "" {
		return "", "", fmt.Errorf("service is required")
	}

	stackName := strings.TrimSpace(request.Stack)
	return serviceName, stackName, nil
}

func findTargetService(serviceRows []service.Info, serviceName string, stackName string) (service.Info, error) {
	matches := make([]service.Info, 0)
	for _, row := range serviceRows {
		if row.Name != serviceName {
			continue
		}
		if stackName != "" && row.Stack != stackName {
			continue
		}
		matches = append(matches, row)
	}

	if len(matches) == 0 {
		if stackName != "" {
			return service.Info{}, fmt.Errorf("service %q in stack %q not found", serviceName, stackName)
		}

		return service.Info{}, fmt.Errorf("service %q not found", serviceName)
	}
	if len(matches) > 1 {
		stackNames := make([]string, 0, len(matches))
		for _, row := range matches {
			stackNames = append(stackNames, row.Stack)
		}

		return service.Info{}, fmt.Errorf(
			"service %q found in multiple stacks (%s); provide stack parameter",
			serviceName,
			strings.Join(stackNames, ", "),
		)
	}

	return matches[0], nil
}

func (p *PingWebRoutes) pingRoute(ctx context.Context, rawAddress string) webRoutePingResult {
	candidateURLs := buildCandidateWebRouteURLs(rawAddress)
	for index, url := range candidateURLs {
		startedAt := time.Now()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return webRoutePingResult{
				URL:        url,
				Success:    false,
				DurationMS: time.Since(startedAt).Milliseconds(),
				Error:      err.Error(),
			}
		}

		response, err := p.client.Do(request)
		durationMS := time.Since(startedAt).Milliseconds()
		if err != nil {
			if index == len(candidateURLs)-1 {
				return webRoutePingResult{
					URL:        url,
					Success:    false,
					DurationMS: durationMS,
					Error:      err.Error(),
				}
			}

			continue
		}

		io.Copy(io.Discard, response.Body) //nolint:errcheck // Best-effort body drain for connection reuse.
		response.Body.Close()

		return webRoutePingResult{
			URL:        url,
			Success:    response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusBadRequest,
			StatusCode: response.StatusCode,
			DurationMS: durationMS,
		}
	}

	return webRoutePingResult{
		Success: false,
		Error:   "no address to ping",
	}
}

func buildCandidateWebRouteURLs(address string) []string {
	normalized := strings.TrimSpace(address)
	if normalized == "" {
		return nil
	}

	if strings.HasPrefix(normalized, "https://") || strings.HasPrefix(normalized, "http://") {
		return []string{normalized}
	}

	return []string{
		"https://" + normalized,
		"http://" + normalized,
	}
}

type webRoutePingResult struct {
	// Stack is a stack where service belongs.
	Stack string `json:"stack"`

	// Service is a service name.
	Service string `json:"service"`

	// Domain is a route domain.
	Domain string `json:"domain"`

	// Address is a route address from service metadata.
	Address string `json:"address"`

	// Port is a route target service port from metadata.
	Port string `json:"port"`

	// URL is a URL that was used for HTTP check.
	URL string `json:"url"`

	// Success reports whether route responded with 2xx/3xx status.
	Success bool `json:"success"`

	// StatusCode is an HTTP status code from route response.
	StatusCode int `json:"status_code,omitempty"`

	// DurationMS is ping duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// Error contains ping error message, when check failed.
	Error string `json:"error,omitempty"`
}
