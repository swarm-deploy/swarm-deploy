package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/artarts36/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/service/webroute"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPingWebRoutesExecute(t *testing.T) {
	address := "routes.example.com"
	tool := NewPingWebRoutes(&fakeServiceStore{
		services: []service.Info{
			{
				Stack: "core",
				Name:  "api",
				WebRoutes: []webroute.Route{
					{
						Domain:  "api.example.com",
						Address: address + "/ok",
						Port:    "8080",
					},
					{
						Domain:  "api.example.com",
						Address: address + "/missing",
						Port:    "8080",
					},
				},
			},
		},
	})
	tool.client = &fakeHTTPDoer{
		responses: map[string]fakeHTTPDoerResponse{
			"https://routes.example.com/ok": {
				err: assert.AnError,
			},
			"http://routes.example.com/ok": {
				statusCode: http.StatusOK,
			},
			"https://routes.example.com/missing": {
				err: assert.AnError,
			},
			"http://routes.example.com/missing": {
				statusCode: http.StatusNotFound,
			},
		},
	}

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"service": "api",
		},
	})
	require.NoError(t, err, "execute service_webroute_ping")

	var payload struct {
		Results []struct {
			Stack      string `json:"stack"`
			Service    string `json:"service"`
			Address    string `json:"address"`
			URL        string `json:"url"`
			Success    bool   `json:"success"`
			StatusCode int    `json:"status_code"`
		} `json:"results"`
	}

	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Results, 2, "expected 2 routes")

	assert.Equal(t, "core", payload.Results[0].Stack, "unexpected stack")
	assert.Equal(t, "api", payload.Results[0].Service, "unexpected service")
	assert.True(t, strings.HasPrefix(payload.Results[0].URL, "http://"), "expected http fallback")
	assert.Equal(t, http.StatusOK, payload.Results[0].StatusCode, "unexpected status")
	assert.True(t, payload.Results[0].Success, "expected success")

	assert.Equal(t, http.StatusNotFound, payload.Results[1].StatusCode, "unexpected status")
	assert.False(t, payload.Results[1].Success, "expected not found as failed ping")
}

func TestPingWebRoutesExecuteWithNilServicesStore(t *testing.T) {
	tool := NewPingWebRoutes(nil)

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"service": "api",
		},
	})
	require.Error(t, err, "expected nil services store error")
	assert.Contains(t, err.Error(), "services store is not configured", "unexpected error")
}

func TestPingWebRoutesExecuteRequiresService(t *testing.T) {
	tool := NewPingWebRoutes(&fakeServiceStore{})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{},
	})
	require.Error(t, err, "expected service required error")
	assert.Contains(t, err.Error(), "service is required", "unexpected error")
}

func TestPingWebRoutesExecuteFailsOnAmbiguousService(t *testing.T) {
	tool := NewPingWebRoutes(&fakeServiceStore{
		services: []service.Info{
			{Stack: "core", Name: "api"},
			{Stack: "edge", Name: "api"},
		},
	})

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"service": "api",
		},
	})
	require.Error(t, err, "expected ambiguous service error")
	assert.Contains(t, err.Error(), "provide stack parameter", "unexpected error")
}

func TestPingWebRoutesExecuteWithStack(t *testing.T) {
	tool := NewPingWebRoutes(&fakeServiceStore{
		services: []service.Info{
			{
				Stack: "core",
				Name:  "api",
				WebRoutes: []webroute.Route{
					{
						Domain:  "api.example.com",
						Address: "core.example.com/ok",
						Port:    "8080",
					},
				},
			},
			{
				Stack: "edge",
				Name:  "api",
				WebRoutes: []webroute.Route{
					{
						Domain:  "api-edge.example.com",
						Address: "edge.example.com/ok",
						Port:    "8080",
					},
				},
			},
		},
	})
	tool.client = &fakeHTTPDoer{
		responses: map[string]fakeHTTPDoerResponse{
			"https://edge.example.com/ok": {
				err: assert.AnError,
			},
			"http://edge.example.com/ok": {
				statusCode: http.StatusOK,
			},
		},
	}

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"service": "api",
			"stack":   "edge",
		},
	})
	require.NoError(t, err, "execute service_webroute_ping with stack")

	var payload struct {
		Results []struct {
			Stack   string `json:"stack"`
			Address string `json:"address"`
		} `json:"results"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response")
	require.Len(t, payload.Results, 1, "expected one route in selected stack")
	assert.Equal(t, "edge", payload.Results[0].Stack, "unexpected stack")
	assert.Equal(t, "edge.example.com/ok", payload.Results[0].Address, "unexpected route")
}

type fakeHTTPDoer struct {
	responses map[string]fakeHTTPDoerResponse
}

func (f *fakeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	response, ok := f.responses[request.URL.String()]
	if !ok {
		return nil, assert.AnError
	}
	if response.err != nil {
		return nil, response.err
	}

	return &http.Response{
		StatusCode: response.statusCode,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

type fakeHTTPDoerResponse struct {
	statusCode int
	err        error
}
