package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/metrics"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service"
)

type capturedChatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int64   `json:"max_tokens"`
}

func TestAssistantRoutingProfiles(t *testing.T) {
	testCases := []struct {
		name          string
		message       string
		route         Route
		expectedTools []string
		excludedTools []string
		expectRAG     bool
	}{
		{
			name:          "cluster",
			message:       "Покажи ноды кластера",
			route:         RouteCluster,
			expectedTools: []string{"swarm_node_list"},
			excludedTools: []string{"service_logs_get", "deploy_sync_trigger"},
		},
		{
			name:          "services",
			message:       "Покажи логи сервиса api",
			route:         RouteServices,
			expectedTools: []string{"service_logs_get"},
			excludedTools: []string{"swarm_node_list", "deploy_sync_trigger"},
			expectRAG:     true,
		},
		{
			name:          "diagnostics",
			message:       "Почему api недоступен после deploy?",
			route:         RouteDiagnostics,
			expectedTools: []string{"history_event_list", "service_logs_get", "docker_network_list", "dns_name_resolve"},
			excludedTools: []string{"deploy_sync_trigger", "service_restart_trigger"},
			expectRAG:     true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeStore{services: []service.Info{{Name: "api", Stack: "core"}}}
			tools := &fakeTools{definitions: assistantTestToolDefinitions()}
			var mu sync.Mutex
			requests := make([]capturedChatRequest, 0, 2)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				request := decodeCapturedChatRequest(t, req)
				mu.Lock()
				requests = append(requests, request)
				call := len(requests)
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				if call == 1 {
					writeChatResponse(t, w, string(testCase.route), nil)
					return
				}
				writeChatResponse(t, w, "done", nil)
			}))
			defer server.Close()

			assistantService := newRoutingTestService(t, server.URL, store, tools, nil)
			response := assistantService.Chat(context.Background(), ChatRequest{Message: testCase.message})
			require.Equal(t, StatusCompleted, response.Status)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, requests, 2)
			assert.Empty(t, requests[0].Tools, "router must not receive tools")
			assert.Equal(t, float64(0), requests[0].Temperature)
			assert.Equal(t, int64(routerMaxTokens), requests[0].MaxTokens)

			toolNames := capturedToolNames(requests[1])
			for _, toolName := range testCase.expectedTools {
				assert.Contains(t, toolNames, toolName)
			}
			for _, toolName := range testCase.excludedTools {
				assert.NotContains(t, toolNames, toolName)
			}
			if testCase.expectRAG {
				assert.Greater(t, store.listCalls.Load(), int64(0))
				assert.True(t, requestContains(requests[1], "Relevant service metadata"))
			} else {
				assert.Equal(t, int64(0), store.listCalls.Load())
				assert.False(t, requestContains(requests[1], "service.store"))
			}
		})
	}
}

func TestAssistantGreetingFastPathUsesNoRouterRAGOrTools(t *testing.T) {
	store := &fakeStore{services: []service.Info{{Name: "api", Stack: "core"}}}
	tools := &fakeTools{definitions: assistantTestToolDefinitions()}
	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		writeChatResponse(t, w, "Привет!", nil)
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, store, tools, nil)
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Привет"})
	require.Equal(t, StatusCompleted, response.Status)
	require.Len(t, requests, 1, "greeting fast-path should skip router completion")
	assert.Empty(t, requests[0].Tools)
	assert.Equal(t, int64(0), store.listCalls.Load())
	assert.False(t, requestContains(requests[0], "service.store"))
	assert.False(t, requestContains(requests[0], "Available tools:"))
}

func TestAssistantRouteToolsIntersectGlobalAllowlist(t *testing.T) {
	store := &fakeStore{}
	tools := &fakeTools{definitions: assistantTestToolDefinitions()}
	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			writeChatResponse(t, w, string(RouteServices), nil)
			return
		}
		writeChatResponse(t, w, "done", nil)
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, store, tools, []string{"service_logs_get", "swarm_node_list"})
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Покажи логи api"})
	require.Equal(t, StatusCompleted, response.Status)
	require.Len(t, requests, 2)
	assert.Equal(t, []string{"service_logs_get"}, capturedToolNames(requests[1]))
}

func TestAssistantRejectsToolOutsideSelectedRouteAtExecution(t *testing.T) {
	store := &fakeStore{}
	tools := &fakeTools{definitions: assistantTestToolDefinitions()}
	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1:
			writeChatResponse(t, w, string(RouteCluster), nil)
		case 2:
			writeChatResponse(t, w, "", []map[string]any{{
				"id": "tool-1", "type": "function",
				"function": map[string]any{"name": "service_logs_get", "arguments": `{}`},
			}})
		default:
			writeChatResponse(t, w, "Tool was rejected.", nil)
		}
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, store, tools, nil)
	response := assistantService.Chat(context.Background(), ChatRequest{Message: "Покажи ноды"})
	require.Equal(t, StatusCompleted, response.Status)
	assert.Empty(t, tools.calls, "out-of-route tool must not reach executor")
	require.Len(t, requests, 3)
	assert.True(t, requestContains(requests[2], "not allowed by the selected assistant route"))
}

func TestAssistantRouterFallbackContinuesMainRequest(t *testing.T) {
	testCases := []struct {
		name          string
		routerContent string
		emptyChoices  bool
	}{
		{name: "unknown route", routerContent: "other"},
		{name: "invalid response", routerContent: "services because the request concerns logs"},
		{name: "router error", emptyChoices: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeStore{services: []service.Info{{Name: "api", Stack: "core"}}}
			tools := &fakeTools{definitions: assistantTestToolDefinitions()}
			var requests []capturedChatRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, decodeCapturedChatRequest(t, req))
				w.Header().Set("Content-Type", "application/json")
				if len(requests) == 1 {
					if testCase.emptyChoices {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
						return
					}
					writeChatResponse(t, w, testCase.routerContent, nil)
					return
				}
				writeChatResponse(t, w, "fallback answer", nil)
			}))
			defer server.Close()

			assistantService := newRoutingTestService(t, server.URL, store, tools, nil)
			response := assistantService.Chat(context.Background(), ChatRequest{Message: "Inspect runtime"})
			require.Equal(t, StatusCompleted, response.Status)
			assert.Equal(t, "fallback answer", response.Answer)
			require.Len(t, requests, 2)
			assert.ElementsMatch(t, capturedToolNames(requests[1]), definitionNames(tools.definitions))
			assert.Greater(t, store.listCalls.Load(), int64(0), "fallback must retain service context")
		})
	}
}

func TestAssistantRouterReceivesRecentHistoryForConfirmation(t *testing.T) {
	store := &fakeStore{}
	tools := &fakeTools{definitions: assistantTestToolDefinitions()}
	var requests []capturedChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, decodeCapturedChatRequest(t, req))
		w.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1, 3:
			writeChatResponse(t, w, string(RouteServices), nil)
		case 2:
			writeChatResponse(t, w, "Restart core/api? Confirm explicitly.", nil)
		default:
			writeChatResponse(t, w, "Confirmed.", nil)
		}
	}))
	defer server.Close()

	assistantService := newRoutingTestService(t, server.URL, store, tools, nil)
	first := assistantService.Chat(context.Background(), ChatRequest{Message: "Перезапусти core/api"})
	require.Equal(t, StatusCompleted, first.Status)
	second := assistantService.Chat(context.Background(), ChatRequest{
		ConversationID: first.ConversationID,
		Message:        "Да",
	})
	require.Equal(t, StatusCompleted, second.Status)
	require.Len(t, requests, 4)
	assert.True(t, requestContains(requests[2], "Перезапусти core/api"))
	assert.True(t, requestContains(requests[2], "Restart core/api? Confirm explicitly."))
	assert.Equal(t, "user", requests[2].Messages[len(requests[2].Messages)-1].Role)
	assert.Equal(t, "Да", requests[2].Messages[len(requests[2].Messages)-1].Content)
	assert.Contains(t, capturedToolNames(requests[3]), "service_restart_trigger")
}

func newRoutingTestService(
	t *testing.T,
	baseURL string,
	store *fakeStore,
	tools *fakeTools,
	allowedTools []string,
) *Service {
	t.Helper()
	assistantService, err := NewService(
		Config{
			Enabled:                 true,
			ModelName:               "test-model",
			BaseURL:                 baseURL,
			APIToken:                "test-token",
			Temperature:             0.3,
			MaxTokens:               64,
			AllowedTools:            allowedTools,
			ConversationInMemoryTTL: time.Hour,
		},
		store,
		tools,
		&dispatcher.NopDispatcher{},
		metrics.NopAssistant{},
	)
	require.NoError(t, err)
	return assistantService
}

func decodeCapturedChatRequest(t *testing.T, req *http.Request) capturedChatRequest {
	t.Helper()
	var captured capturedChatRequest
	require.NoError(t, json.NewDecoder(req.Body).Decode(&captured))
	return captured
}

func writeChatResponse(t *testing.T, w http.ResponseWriter, content string, toolCalls []map[string]any) {
	t.Helper()
	message := map[string]any{"content": content}
	if toolCalls != nil {
		message["tool_calls"] = toolCalls
	}
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": message}},
		"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 1, "total_tokens": 4},
	}))
}

func capturedToolNames(request capturedChatRequest) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Function.Name)
	}
	slices.Sort(names)
	return names
}

func requestContains(request capturedChatRequest, text string) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func definitionNames(definitions []routing.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func assistantTestToolDefinitions() []routing.ToolDefinition {
	names := []string{
		"deploy_sync_trigger",
		"history_event_list",
		"recommendation_list",
		"git_commit_list",
		"git_commit_diff",
		"swarm_node_list",
		"docker_network_list",
		"docker_plugin_list",
		"docker_secret_list",
		"service_logs_get",
		"service_spec_get",
		"service_replicas_set",
		"service_restart_trigger",
		"service_webroute_ping",
		"dependency_graph_get",
		"registry_image_version_get",
		"external_repository_release_latest_get",
		"dns_name_resolve",
		"date",
		"self_metrics_list",
		"assistant_prompt_injection_report",
	}
	definitions := make([]routing.ToolDefinition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, routing.ToolDefinition{
			Name:        name,
			Description: name,
			ParametersJSONSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}
	return definitions
}
